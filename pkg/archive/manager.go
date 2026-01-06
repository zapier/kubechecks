package archive

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/vcs"
)

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

	// Extract merge commit SHA from archive URL for cache key
	// Archive URLs contain the SHA: https://github.com/owner/repo/archive/{sha}.zip
	// IMPORTANT: Must use merge commit SHA, not HEAD SHA, as cache key!
	// Otherwise, cache returns stale archives when new commits are pushed to existing PR
	mergeCommitSHA, err := extractSHAFromArchiveURL(archiveURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to extract merge commit SHA from archive URL")
	}

	log.Debug().
		Caller().
		Str("archive_url", archiveURL).
		Str("merge_commit_sha", mergeCommitSHA).
		Str("head_sha", pr.SHA).
		Msg("extracted merge commit SHA from archive URL")

	// Get authentication headers for archive download
	authHeaders := m.vcsClient.GetAuthHeaders()

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

// extractSHAFromArchiveURL extracts the commit SHA from an archive URL
// Supports GitHub and GitLab archive URL formats:
// - GitHub: https://github.com/owner/repo/archive/{sha}.zip
// - GitLab: https://gitlab.com/api/v4/projects/{encoded}/repository/archive.zip?sha={ref}
func extractSHAFromArchiveURL(archiveURL string) (string, error) {
	// Try GitHub format first: /archive/{sha}.zip or /archive/{sha}.tar.gz
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
