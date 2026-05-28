package archive

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/vcs"
)

// urlParseError wraps failures from extractSHAFromArchiveURL. These are permanent —
// the archive URL format is unrecognized and retrying won't help.
type urlParseError struct{ err error }

func (e *urlParseError) Error() string { return e.err.Error() }
func (e *urlParseError) Unwrap() error { return e.err }

// authError wraps failures from vcs.Client.GetAuthHeaders. These are config/permission
// issues (missing credentials, bad App private key, JWT exchange failure) rather than
// transient network blips — retrying via replan won't make the credentials valid.
// Without this typing, such failures would fall through PostArchiveErrorMessage's
// catch-all and show a misleading "transient error, comment to retry" message.
type authError struct{ err error }

func (e *authError) Error() string { return e.err.Error() }
func (e *authError) Unwrap() error { return e.err }

// Manager manages archive-based repository access
// It provides a similar interface to git.RepoManager but uses VCS archives instead
type Manager struct {
	cache     *Cache
	vcsClient vcs.Client
	cfg       config.ServerConfig
}

// NewManager creates a new archive manager
func NewManager(cfg config.ServerConfig, vcsClient vcs.Client) *Manager {
	cacheConfig := Config{
		BaseDir: cfg.ArchiveCacheDir,
		TTL:     cfg.ArchiveCacheTTL,
	}

	return &Manager{
		cache:     NewCache(cacheConfig),
		vcsClient: vcsClient,
		cfg:       cfg,
	}
}

// Clone retrieves a repository using VCS archive downloads
// Returns a git.Repo-like structure pointing to the extracted archive
// Note: This satisfies a similar interface to git.RepoManager.Clone
func (m *Manager) Clone(ctx context.Context, cloneURL, branchName string, pr vcs.PullRequest) (*git.Repo, error) {
	log.Info().
		Str("clone_url", cloneURL).
		Str("branch", branchName).
		Int("pr_number", pr.CheckID).
		Msg("using archive mode instead of git clone")

	// Get archive URL from VCS
	archiveURL, err := m.vcsClient.DownloadArchive(ctx, pr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get archive URL from VCS")
	}

	// Extract merge commit SHA from archive URL for cache key.
	// Archive URLs contain the SHA, e.g.:
	//   GitHub: https://api.github.com/repos/owner/repo/zipball/{sha}
	//   GitLab: https://gitlab.com/api/v4/projects/{id}/repository/archive.zip?sha={ref}
	// IMPORTANT: Must use merge commit SHA, not HEAD SHA, as cache key!
	// Otherwise, cache returns stale archives when new commits are pushed to existing PR.
	mergeCommitSHA, err := extractSHAFromArchiveURL(archiveURL)
	if err != nil {
		return nil, &urlParseError{err: errors.Wrap(err, "failed to extract merge commit SHA from archive URL")}
	}

	log.Debug().
		Caller().
		Str("archive_url", archiveURL).
		Str("merge_commit_sha", mergeCommitSHA).
		Str("head_sha", pr.SHA).
		Msg("extracted merge commit SHA from archive URL")

	// Get authentication headers for archive download. For GitHub App auth this fetches
	// a fresh installation token, so the call needs the request context.
	authHeaders, err := m.vcsClient.GetAuthHeaders(ctx)
	if err != nil {
		return nil, &authError{err: errors.Wrap(err, "failed to get archive auth headers")}
	}

	// Download and extract archive (or get from cache)
	extractedPath, err := m.cache.GetOrDownload(ctx, archiveURL, mergeCommitSHA, authHeaders)
	if err != nil {
		return nil, errors.Wrap(err, "failed to download and extract archive")
	}

	// Create a git.Repo structure pointing to the extracted archive
	// This allows existing code to work with the archive as if it were a git repo
	repo := &git.Repo{
		BranchName:     branchName,
		Config:         m.cfg,
		CloneURL:       cloneURL,
		Directory:      extractedPath,
		BaseBranchName: branchName,
		// Note: No TempBranch needed - archives are immutable snapshots
	}

	log.Info().
		Str("clone_url", cloneURL).
		Str("extracted_path", extractedPath).
		Msg("archive downloaded and ready")

	return repo, nil
}

// GetChangedFiles retrieves the list of changed files from VCS API
// This replaces the git diff operation
func (m *Manager) GetChangedFiles(ctx context.Context, pr vcs.PullRequest) ([]string, error) {
	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Msg("fetching changed files from VCS API")

	files, err := m.vcsClient.GetPullRequestFiles(ctx, pr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get PR files from VCS")
	}

	log.Info().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Int("file_count", len(files)).
		Msg("fetched changed files from VCS API")

	return files, nil
}

// Cleanup is a no-op for archive manager (cache has its own TTL-based cleanup)
func (m *Manager) Cleanup() {
	log.Debug().Caller().Msg("archive manager: cleanup is managed by background TTL routine")
}

// Shutdown stops the archive cache
func (m *Manager) Shutdown() {
	log.Info().Msg("shutting down archive manager")
	m.cache.Shutdown()
}

// Release releases a reference to an archive
func (m *Manager) Release(cloneURL, mergeCommitSHA string) {
	m.cache.Release(cloneURL, mergeCommitSHA)
}

// ValidatePullRequest checks if a PR is suitable for archive mode
// Returns error if PR has conflicts or is not mergeable
func (m *Manager) ValidatePullRequest(ctx context.Context, pr vcs.PullRequest) error {
	// Try to get archive URL - this will fail if PR has conflicts
	_, err := m.vcsClient.DownloadArchive(ctx, pr)
	if err != nil {
		return errors.Wrap(err, "PR cannot use archive mode (may have conflicts)")
	}
	return nil
}

// PostConflictMessage posts a message to the PR when it has conflicts
func (m *Manager) PostConflictMessage(ctx context.Context, pr vcs.PullRequest) error {
	message := fmt.Sprintf("⚠️ This request cannot be checked.\n\n"+
		"PR/MR may be in draft, or resolve conflicts with the base branch (%s) before running checks.\n\n"+
		"To re-trigger checks after resolving conflicts, comment `%s`.",
		pr.BaseRef,
		m.cfg.ReplanCommentMessage,
	)

	_, err := m.vcsClient.PostMessage(ctx, pr, message)
	if err != nil {
		return errors.Wrap(err, "failed to post conflict message")
	}

	log.Info().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Msg("posted conflict resolution message")

	return nil
}

// PostArchiveErrorMessage posts a specific error message to the PR based on the type of download failure.
// If the original context is already cancelled (e.g. timeout), a background context is used so the
// message can still be delivered.
func (m *Manager) PostArchiveErrorMessage(ctx context.Context, pr vcs.PullRequest, cloneErr error) error {
	replan := m.cfg.ReplanCommentMessage

	// Classify by the error itself first; ctx.Err() is a fallback for cases where the
	// context was cancelled for an unrelated reason after Clone returned.
	var urlErr *urlParseError
	var authErr *authError
	var httpErr *HTTPError
	hasHTTP := errors.As(cloneErr, &httpErr)

	// Shared message strings — used in both the cloneErr branch and the ctx.Err() fallback
	// so that wording stays consistent if either is ever updated.
	timedOutMsg := fmt.Sprintf(
		"⚠️ Kubechecks timed out waiting for the repository archive to be ready.\n\n"+
			"The VCS may still be computing the merge result. Comment `%s` to retry.",
		replan,
	)
	interruptedMsg := fmt.Sprintf(
		"⚠️ The archive download was interrupted before completing.\n\n"+
			"This is usually caused by a shutdown or restart. Comment `%s` to retry.",
		replan,
	)

	var message string
	switch {
	case errors.Is(cloneErr, context.DeadlineExceeded):
		message = timedOutMsg

	case errors.Is(cloneErr, context.Canceled):
		message = interruptedMsg

	case hasHTTP && httpErr.StatusCode == http.StatusNotFound:
		message = fmt.Sprintf(
			"⚠️ Repository archive not found (HTTP 404).\n\n"+
				"The VCS may still be preparing the merged archive. Comment `%s` to retry.",
			replan,
		)

	case hasHTTP && httpErr.StatusCode == http.StatusTooManyRequests:
		message = fmt.Sprintf(
			"⚠️ Rate limited while downloading the repository archive (HTTP 429).\n\n"+
				"Comment `%s` to retry.",
			replan,
		)

	case hasHTTP && httpErr.StatusCode >= http.StatusInternalServerError:
		message = fmt.Sprintf(
			"⚠️ VCS server error while downloading the repository archive (HTTP %d %s).\n\n"+
				"The VCS may be experiencing issues. Comment `%s` to retry.",
			httpErr.StatusCode, http.StatusText(httpErr.StatusCode), replan,
		)

	case hasHTTP && httpErr.StatusCode == http.StatusUnauthorized:
		// 401 = authentication failure (missing or invalid token)
		message = "⚠️ Authentication failed downloading the repository archive (HTTP 401 Unauthorized).\n\n" +
			"Check that kubechecks has a valid VCS token configured."

	case hasHTTP && httpErr.StatusCode == http.StatusForbidden:
		// 403 = authorization failure (token valid but lacks required scope/permissions)
		message = "⚠️ Access denied downloading the repository archive (HTTP 403 Forbidden).\n\n" +
			"Check that the kubechecks VCS token has sufficient repository permissions."

	case errors.As(cloneErr, &authErr):
		// Auth header construction failed before any HTTP call (e.g. no credentials
		// configured, GitHub App installation token fetch failed, malformed PEM).
		// Permanent — retrying via replan won't fix the configuration.
		message = "⚠️ Kubechecks could not obtain credentials to download the repository archive.\n\n" +
			"This is a configuration problem — check that the kubechecks VCS token or " +
			"GitHub App credentials are valid. Comment `" + replan + "` will not help until " +
			"the configuration is fixed."

	case errors.As(cloneErr, &urlErr):
		// Unrecognized archive URL format — this is a bug, not something the user can retry
		message = "⚠️ Kubechecks could not parse the archive URL returned by the VCS.\n\n" +
			"This is likely a configuration or VCS compatibility issue — check the kubechecks logs."

	case hasHTTP && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500:
		// Any remaining 4xx not handled above (400, 405, 410, 422, etc.) is a permanent
		// client error — retrying via replan won't change the outcome.
		message = fmt.Sprintf(
			"⚠️ Failed to download the repository archive (HTTP %d %s).\n\n"+
				"This is a permanent error. Check the kubechecks logs for details.",
			httpErr.StatusCode, http.StatusText(httpErr.StatusCode),
		)

	case hasHTTP:
		// Unexpected non-4xx, non-5xx response (e.g. 3xx not followed as redirect).
		message = fmt.Sprintf(
			"⚠️ Failed to download the repository archive (HTTP %d %s).\n\n"+
				"Comment `%s` to retry.",
			httpErr.StatusCode, http.StatusText(httpErr.StatusCode), replan,
		)

	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		message = timedOutMsg

	case ctx.Err() != nil:
		message = interruptedMsg

	default:
		message = fmt.Sprintf(
			"⚠️ Failed to download the repository archive due to a transient error.\n\n"+
				"Comment `%s` to retry.",
			replan,
		)
	}

	// Use a fresh context if the original was cancelled so the message is still delivered.
	// A 30s timeout bounds the fallback — a hung VCS should not leak this goroutine indefinitely.
	postCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		postCtx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
	}

	_, err := m.vcsClient.PostMessage(postCtx, pr, message)
	if err != nil {
		return errors.Wrap(err, "failed to post archive error message")
	}

	log.Info().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Msg("posted archive download error message")

	return nil
}

// extractSHAFromArchiveURL extracts the commit SHA from an archive URL
// Supports GitHub and GitLab archive URL formats:
// - GitHub REST API zipball: https://api.github.com/repos/owner/repo/zipball/{sha}
// - GitHub web archive (legacy): https://github.com/owner/repo/archive/{sha}.zip
// - GitLab: https://gitlab.com/api/v4/projects/{encoded}/repository/archive.zip?sha={ref}
func extractSHAFromArchiveURL(archiveURL string) (string, error) {
	// GitHub REST API zipball/tarball: /zipball/{sha} or /tarball/{sha} (no extension)
	for _, marker := range []string{"/zipball/", "/tarball/"} {
		if !strings.Contains(archiveURL, marker) {
			continue
		}
		parts := strings.Split(archiveURL, marker)
		// Strip any query string that may follow the SHA.
		sha := strings.SplitN(parts[len(parts)-1], "?", 2)[0]
		if sha == "" {
			return "", fmt.Errorf("empty SHA extracted from archive URL: %s", archiveURL)
		}
		return sha, nil
	}

	// GitHub web archive (legacy): /archive/{sha}.zip or /archive/{sha}.tar.gz
	if strings.Contains(archiveURL, "/archive/") {
		// Extract filename from URL path
		parts := strings.Split(archiveURL, "/archive/")
		if len(parts) < 2 {
			return "", fmt.Errorf("invalid GitHub archive URL format: %s", archiveURL)
		}

		// Get the SHA part (e.g., "abc123.zip" -> "abc123")
		filename := parts[len(parts)-1]
		sha := strings.TrimSuffix(filename, filepath.Ext(filename))

		if sha == "" {
			return "", fmt.Errorf("empty SHA extracted from archive URL: %s", archiveURL)
		}

		return sha, nil
	}

	// Try GitLab format: ?sha={ref}
	if strings.Contains(archiveURL, "?sha=") || strings.Contains(archiveURL, "&sha=") {
		// Extract SHA from query parameter
		parts := strings.Split(archiveURL, "sha=")
		if len(parts) < 2 {
			return "", fmt.Errorf("invalid GitLab archive URL format: %s", archiveURL)
		}

		// Get SHA (handle case where there might be more query params after)
		sha := strings.Split(parts[1], "&")[0]

		if sha == "" {
			return "", fmt.Errorf("empty SHA extracted from archive URL: %s", archiveURL)
		}

		return sha, nil
	}

	return "", fmt.Errorf("unrecognized archive URL format: %s", archiveURL)
}
