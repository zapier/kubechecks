package archive

import (
	"context"
	"fmt"

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

	// Get merge commit SHA (for cache key)
	// The archive URL contains the commit SHA, extract it or use a placeholder
	mergeCommitSHA := pr.SHA // Fallback to PR SHA

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
	log.Debug().Msg("archive manager: cleanup is managed by background TTL routine")
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
	message := fmt.Sprintf("⚠️ This PR has merge conflicts and cannot be checked.\n\n"+
		"Please resolve conflicts with the base branch (%s) before running checks.\n\n"+
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
