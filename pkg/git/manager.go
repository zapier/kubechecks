package git

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"golang.org/x/sync/singleflight"

	"github.com/zapier/kubechecks/pkg/config"
)

var tracer = otel.Tracer("pkg/git")

// RepoManager is the interface that both implementations satisfy
type RepoManager interface {
	Clone(ctx context.Context, cloneUrl, branchName string) (*Repo, error)
	Cleanup()
	Shutdown()
}

// PersistentRepoManager manages a persistent cache of git repositories
type PersistentRepoManager struct {
	lock          sync.RWMutex
	repos         map[string]*PersistentRepo
	persistentDir string
	cfg           config.ServerConfig
	cleanupTicker *time.Ticker
	done          chan struct{}

	// Prevents thundering herd - ensures only one clone per repo happens
	cloneGroup singleflight.Group
}

// PersistentRepo wraps a Repo with metadata for caching
// Note: No lock needed as queue system ensures sequential processing per repo
type PersistentRepo struct {
	*Repo
	lastUsed   time.Time
	refCount   int32
	baseBranch string
}

// EphemeralRepoManager is the legacy implementation (per-request repos)
type EphemeralRepoManager struct {
	lock  sync.Mutex
	repos []*Repo
	cfg   config.ServerConfig
}

// NewRepoManager creates a new repository manager based on configuration
// If RepoCacheEnabled is true, returns PersistentRepoManager
// Otherwise returns EphemeralRepoManager for backward compatibility
func NewRepoManager(cfg config.ServerConfig) RepoManager {
	if cfg.RepoCacheEnabled {
		return NewPersistentRepoManager(cfg, cfg.RepoCacheDir)
	}
	return NewEphemeralRepoManager(cfg)
}

// NewPersistentRepoManager creates a persistent repository cache manager
func NewPersistentRepoManager(cfg config.ServerConfig, persistentDir string) *PersistentRepoManager {
	rm := &PersistentRepoManager{
		repos:         make(map[string]*PersistentRepo),
		persistentDir: persistentDir,
		cfg:           cfg,
		done:          make(chan struct{}),
	}

	// Create persistent directory if it doesn't exist
	if err := os.MkdirAll(persistentDir, 0755); err != nil {
		log.Fatal().Err(err).Str("dir", persistentDir).Msg("failed to create persistent repo directory")
	}

	log.Info().
		Str("dir", persistentDir).
		Str("ttl", cfg.RepoCacheTTL.String()).
		Msg("persistent repo cache enabled")

	// Start background cleanup goroutine
	go rm.startCleanupRoutine()

	// Start metrics update routine
	go rm.startMetricsUpdateRoutine()

	return rm
}

// NewEphemeralRepoManager creates a legacy ephemeral repository manager
func NewEphemeralRepoManager(cfg config.ServerConfig) *EphemeralRepoManager {
	return &EphemeralRepoManager{
		cfg: cfg,
	}
}

// Clone implements RepoManager interface for PersistentRepoManager
// Creates isolated temp branch for each PR check
// Note: Sequential processing per repo is now handled by the queue system
func (rm *PersistentRepoManager) Clone(ctx context.Context, cloneUrl, branchName string) (*Repo, error) {
	ctx, span := tracer.Start(ctx, "PersistentRepoManager.Clone")
	defer span.End()

	// Get or create the persistent repo
	pr, err := rm.GetOrCloneRepo(ctx, cloneUrl, branchName)
	if err != nil {
		return nil, err
	}

	// Update base branch to latest
	if err := rm.UpdateBaseBranch(ctx, pr, branchName); err != nil {
		return nil, errors.Wrap(err, "failed to update base branch")
	}

	// Create temporary branch for this PR check
	prIdentifier := fmt.Sprintf("%d", time.Now().UnixNano())
	commitSHA, err := pr.GetCurrentCommitSHA()
	if err != nil {
		// If we can't get SHA, use a fallback
		commitSHA = "00000000"
	}

	tempBranch, err := pr.CreateTempBranch(ctx, prIdentifier, commitSHA)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create temp branch")
	}

	// Create a copy of the Repo for this specific check
	repoCopy := &Repo{
		BranchName:     pr.BranchName,
		Config:         pr.Config,
		CloneURL:       pr.CloneURL,
		Directory:      pr.Directory,
		TempBranch:     tempBranch,
		BaseBranchName: branchName,
	}

	log.Info().
		Str("url", cloneUrl).
		Str("temp_branch", tempBranch).
		Str("base_branch", branchName).
		Msg("created isolated temp branch for PR check")

	return repoCopy, nil
}

// GetOrCloneRepo retrieves a cached repo or clones it if not present
func (rm *PersistentRepoManager) GetOrCloneRepo(ctx context.Context, cloneURL, baseBranch string) (*PersistentRepo, error) {
	ctx, span := tracer.Start(ctx, "GetOrCloneRepo")
	defer span.End()

	repoKey := generateRepoKey(cloneURL)

	// Use singleflight to prevent duplicate clones for the same repo
	result, err, _ := rm.cloneGroup.Do(repoKey, func() (interface{}, error) {
		// Check if repo already exists in cache
		rm.lock.RLock()
		pr, exists := rm.repos[repoKey]
		rm.lock.RUnlock()

		if exists {
			// Cache hit - reuse existing repo
			repoCacheHits.Inc()
			log.Debug().
				Str("url", cloneURL).
				Str("branch", baseBranch).
				Msg("using cached repository")
			return pr, nil
		}

		// Cache miss - need to clone
		repoCacheMisses.Inc()
		repoCloneTotal.Inc()

		// Clone the repository to persistent storage
		log.Info().
			Str("url", cloneURL).
			Str("branch", baseBranch).
			Msg("cloning repository to persistent cache")

		repoDir := filepath.Join(rm.persistentDir, sanitizeRepoName(cloneURL))

		// Create the repo object
		repo := New(rm.cfg, cloneURL, baseBranch)

		// Record clone duration
		timer := prometheus.NewTimer(repoCloneDuration)
		defer timer.ObserveDuration()

		// First clone to temp, then move to persistent location
		// This avoids issues with execGitCommand trying to chdir to non-existent directory
		if err := repo.Clone(ctx); err != nil {
			repoCloneFailed.Inc()
			return nil, errors.Wrap(err, "failed to clone repository to persistent cache")
		}
		repoCloneSuccess.Inc()

		// Move from temp to persistent location
		tempDir := repo.Directory
		if err := os.Rename(tempDir, repoDir); err != nil {
			// If rename fails, try to remove temp and return error
			if removeErr := os.RemoveAll(tempDir); removeErr != nil {
				log.Warn().Err(removeErr).Str("dir", tempDir).Msg("failed to remove temp directory after rename failure")
			}
			return nil, errors.Wrapf(err, "failed to move repository from %s to %s", tempDir, repoDir)
		}
		repo.Directory = repoDir

		// Create persistent repo wrapper
		pr = &PersistentRepo{
			Repo:       repo,
			lastUsed:   time.Now(),
			refCount:   0,
			baseBranch: baseBranch,
		}

		// Add to cache
		rm.lock.Lock()
		rm.repos[repoKey] = pr
		rm.lock.Unlock()

		log.Info().
			Str("url", cloneURL).
			Str("path", repoDir).
			Msg("repository cloned to persistent cache")

		return pr, nil
	})

	if err != nil {
		return nil, err
	}

	return result.(*PersistentRepo), nil
}

// UpdateBaseBranch updates a cached repository's base branch to latest
// Note: No locking needed as queue system ensures sequential processing per repo
func (rm *PersistentRepoManager) UpdateBaseBranch(ctx context.Context, pr *PersistentRepo, baseBranch string) error {
	ctx, span := tracer.Start(ctx, "UpdateBaseBranch")
	defer span.End()

	log.Debug().
		Str("url", pr.CloneURL).
		Str("branch", baseBranch).
		Str("path", pr.Directory).
		Msg("updating base branch in cached repository")

	// Checkout the base branch if different (and not empty)
	if baseBranch != "" && pr.BranchName != baseBranch {
		log.Debug().
			Str("from", pr.BranchName).
			Str("to", baseBranch).
			Msg("checking out different branch")

		if err := pr.Repo.Checkout(baseBranch); err != nil {
			return errors.Wrapf(err, "failed to checkout branch %s", baseBranch)
		}
		pr.BranchName = baseBranch
	}

	// Pull latest changes
	if err := pr.Update(ctx); err != nil {
		return errors.Wrap(err, "failed to update base branch")
	}

	pr.baseBranch = baseBranch
	pr.lastUsed = time.Now()

	log.Debug().
		Str("url", pr.CloneURL).
		Str("branch", baseBranch).
		Msg("base branch updated successfully")

	return nil
}

// ReleaseRepo decrements the reference count for a repository
func (rm *PersistentRepoManager) ReleaseRepo(cloneURL string) {
	repoKey := generateRepoKey(cloneURL)

	rm.lock.RLock()
	pr, exists := rm.repos[repoKey]
	rm.lock.RUnlock()

	if !exists {
		return
	}

	// Decrement reference count
	newCount := atomic.AddInt32(&pr.refCount, -1)
	pr.lastUsed = time.Now()

	log.Debug().
		Str("url", cloneURL).
		Int32("ref_count", newCount).
		Msg("released repository reference")
}

// CleanupTempBranchForRepo cleans up a temp branch after a check completes
func (rm *PersistentRepoManager) CleanupTempBranchForRepo(ctx context.Context, repo *Repo) error {
	if repo.TempBranch == "" {
		// No temp branch to clean up
		return nil
	}

	repoKey := generateRepoKey(repo.CloneURL)

	rm.lock.RLock()
	pr, exists := rm.repos[repoKey]
	rm.lock.RUnlock()

	if !exists {
		log.Warn().
			Str("url", repo.CloneURL).
			Str("temp_branch", repo.TempBranch).
			Msg("persistent repo not found for temp branch cleanup")
		return nil
	}

	// Clean up the temp branch
	// Note: No locking needed as queue system ensures sequential processing
	if err := pr.CleanupTempBranch(ctx, repo.TempBranch, repo.BaseBranchName); err != nil {
		log.Warn().
			Err(err).
			Str("temp_branch", repo.TempBranch).
			Msg("failed to cleanup temp branch")
		// Don't return error - just log it
	}

	// Decrement reference count
	newCount := atomic.AddInt32(&pr.refCount, -1)
	pr.lastUsed = time.Now()

	log.Debug().
		Str("url", repo.CloneURL).
		Str("temp_branch", repo.TempBranch).
		Int32("ref_count", newCount).
		Msg("cleaned up temp branch and released repo")

	return nil
}

// Clone implements RepoManager interface for EphemeralRepoManager
func (rm *EphemeralRepoManager) Clone(ctx context.Context, cloneUrl, branchName string) (*Repo, error) {
	repo := New(rm.cfg, cloneUrl, branchName)

	if err := repo.Clone(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to clone repository")
	}

	rm.lock.Lock()
	defer rm.lock.Unlock()
	rm.repos = append(rm.repos, repo)

	return repo, nil
}

// Cleanup implements RepoManager interface for PersistentRepoManager
func (rm *PersistentRepoManager) Cleanup() {
	// Persistent repos are not cleaned up per-request
	// They're cleaned up by background goroutine based on TTL
	log.Debug().Msg("persistent repo manager: cleanup is no-op (managed by background routine)")
}

// Cleanup implements RepoManager interface for EphemeralRepoManager
func (rm *EphemeralRepoManager) Cleanup() {
	rm.lock.Lock()
	defer rm.lock.Unlock()

	for _, repo := range rm.repos {
		repo.Wipe()
	}
}

// Shutdown stops the background cleanup routine for PersistentRepoManager
func (rm *PersistentRepoManager) Shutdown() {
	log.Info().Msg("shutting down persistent repo manager")
	close(rm.done)
	if rm.cleanupTicker != nil {
		rm.cleanupTicker.Stop()
	}
}

// Shutdown implements RepoManager interface for EphemeralRepoManager
func (rm *EphemeralRepoManager) Shutdown() {
	// No-op for ephemeral manager
	log.Debug().Msg("ephemeral repo manager: shutdown is no-op")
}

// startCleanupRoutine runs background cleanup for stale repos
// runs every 15m and trigger cleanupStaleRepos.
func (rm *PersistentRepoManager) startCleanupRoutine() {
	rm.cleanupTicker = time.NewTicker(15 * time.Minute)
	defer rm.cleanupTicker.Stop()

	for {
		select {
		case <-rm.cleanupTicker.C:
			rm.cleanupStaleRepos()
		case <-rm.done:
			return
		}
	}
}

// cleanupStaleRepos removes repos that haven't been used recently
func (rm *PersistentRepoManager) cleanupStaleRepos() {
	log.Debug().Msg("starting cleanup of stale repositories")

	rm.lock.Lock()
	defer rm.lock.Unlock()

	now := time.Now()
	ttl := rm.cfg.RepoCacheTTL
	var removed int

	for key, pr := range rm.repos {
		// Skip if repo is currently in use
		if atomic.LoadInt32(&pr.refCount) > 0 {
			continue
		}

		// Check if repo is stale
		if now.Sub(pr.lastUsed) > ttl {
			log.Info().
				Str("url", pr.CloneURL).
				Dur("unused_for", now.Sub(pr.lastUsed)).
				Msg("removing stale repository from cache")

			pr.Wipe()
			delete(rm.repos, key)
			removed++
		}
	}

	if removed > 0 {
		log.Info().
			Int("removed", removed).
			Int("remaining", len(rm.repos)).
			Msg("cleanup completed")
	}
}

// Helper functions

// generateRepoKey creates a normalized cache key from clone URL
func generateRepoKey(cloneURL string) string {
	// Normalize URL (remove trailing .git, lowercase)
	normalized := strings.TrimSuffix(strings.ToLower(cloneURL), ".git")
	return normalized
}

// sanitizeRepoName creates a safe directory name from clone URL using MD5 hash
func sanitizeRepoName(cloneURL string) string {
	// Use MD5 hash for simple, deterministic, and collision-resistant directory names
	// Output: 32 hex characters (e.g., "c50de427c98fda747bb0bf6f07571e08")
	hash := md5.Sum([]byte(cloneURL))
	return fmt.Sprintf("%x", hash)
}
