package archive

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/singleflight"
)

// Cache manages a persistent cache of downloaded and extracted archives
type Cache struct {
	lock       sync.RWMutex
	entries    map[string]*CacheEntry
	baseDir    string
	ttl        time.Duration
	downloader *Downloader
	done       chan struct{}

	// Prevents thundering herd - ensures only one download per archive happens
	downloadGroup singleflight.Group
}

// CacheEntry represents a cached archive
type CacheEntry struct {
	extractedPath string
	lastUsed      time.Time
	refCount      int32
}

// Config holds cache configuration
type Config struct {
	BaseDir string        // Base directory for cache (e.g., /tmp/kubechecks/archives)
	TTL     time.Duration // Time-to-live for cached archives
}

// NewCache creates a new archive cache
func NewCache(cfg Config) *Cache {
	if cfg.TTL == 0 {
		cfg.TTL = 1 * time.Hour // Default TTL
	}

	cache := &Cache{
		entries:    make(map[string]*CacheEntry),
		baseDir:    cfg.BaseDir,
		ttl:        cfg.TTL,
		downloader: NewDownloader(),
		done:       make(chan struct{}),
	}

	// Create base directory
	if err := os.MkdirAll(cfg.BaseDir, 0755); err != nil {
		log.Fatal().Err(err).Str("dir", cfg.BaseDir).Msg("failed to create archive cache directory")
	}

	log.Info().
		Str("dir", cfg.BaseDir).
		Str("ttl", cfg.TTL.String()).
		Msg("archive cache enabled")

	// Start background cleanup routine
	go cache.startCleanupRoutine()

	// Start metrics update routine
	go cache.startMetricsUpdateRoutine()

	return cache
}

// GetOrDownload retrieves a cached archive or downloads it if not present
// Returns the path to the extracted archive directory
func (c *Cache) GetOrDownload(ctx context.Context, archiveURL, mergeCommitSHA string, authHeaders map[string]string) (string, error) {

	// Use singleflight to prevent duplicate downloads for the same archive
	result, err, _ := c.downloadGroup.Do(mergeCommitSHA, func() (interface{}, error) {
		// Check if archive already exists in cache
		c.lock.RLock()
		entry, exists := c.entries[mergeCommitSHA]
		c.lock.RUnlock()

		if exists {
			// Cache hit - reuse existing archive
			archiveCacheHits.Inc()
			atomic.AddInt32(&entry.refCount, 1)
			entry.lastUsed = time.Now()

			log.Debug().
				Caller().
				Str("archive_url", archiveURL).
				Str("merge_commit_sha", mergeCommitSHA).
				Str("path", entry.extractedPath).
				Msg("using cached archive")

			return entry.extractedPath, nil
		}

		// Cache miss - need to download
		archiveCacheMisses.Inc()

		log.Info().
			Str("archive_url", archiveURL).
			Str("merge_commit_sha", mergeCommitSHA).
			Msg("downloading archive to cache")

		// Create target directory for this archive
		// Using merge_commit_sha directly as it's globally unique
		targetDir := filepath.Join(c.baseDir, mergeCommitSHA)

		// Download and extract
		extractedPath, err := c.downloader.DownloadAndExtract(ctx, archiveURL, targetDir, authHeaders)
		if err != nil {
			return nil, errors.Wrap(err, "failed to download and extract archive")
		}

		// Create cache entry
		entry = &CacheEntry{
			extractedPath: extractedPath,
			lastUsed:      time.Now(),
			refCount:      1,
		}

		// Add to cache
		c.lock.Lock()
		c.entries[mergeCommitSHA] = entry
		c.lock.Unlock()

		log.Info().
			Str("archive_url", archiveURL).
			Str("merge_commit_sha", mergeCommitSHA).
			Str("path", extractedPath).
			Msg("archive downloaded and cached")

		return extractedPath, nil
	})

	if err != nil {
		return "", err
	}

	return result.(string), nil
}

// Release decrements the reference count for an archive
func (c *Cache) Release(archiveURL, mergeCommitSHA string) {

	c.lock.RLock()
	entry, exists := c.entries[mergeCommitSHA]
	c.lock.RUnlock()

	if !exists {
		return
	}

	newCount := atomic.AddInt32(&entry.refCount, -1)
	entry.lastUsed = time.Now()

	log.Debug().
		Caller().
		Str("archive_url", archiveURL).
		Str("merge_commit_sha", mergeCommitSHA).
		Int32("ref_count", newCount).
		Msg("released archive reference")
}

// Shutdown stops the background cleanup routine
func (c *Cache) Shutdown() {
	log.Info().Msg("shutting down archive cache")
	close(c.done)
}

// startCleanupRoutine runs background cleanup for stale archives
func (c *Cache) startCleanupRoutine() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanupStaleArchives()
		case <-c.done:
			return
		}
	}
}

// cleanupStaleArchives removes archives that haven't been used recently
func (c *Cache) cleanupStaleArchives() {
	log.Debug().Caller().Msg("starting cleanup of stale archives")

	c.lock.Lock()
	defer c.lock.Unlock()

	now := time.Now()
	var removed int

	for key, entry := range c.entries {
		// Skip if archive is currently in use
		if atomic.LoadInt32(&entry.refCount) > 0 {
			continue
		}

		// Check if archive is stale
		if now.Sub(entry.lastUsed) > c.ttl {
			log.Info().
				Str("path", entry.extractedPath).
				Dur("unused_for", now.Sub(entry.lastUsed)).
				Msg("removing stale archive from cache")

			// Remove from disk
			if err := os.RemoveAll(entry.extractedPath); err != nil {
				log.Warn().Err(err).Str("path", entry.extractedPath).Msg("failed to remove archive directory")
			}

			delete(c.entries, key)
			removed++
		}
	}

	if removed > 0 {
		log.Info().
			Int("removed", removed).
			Int("remaining", len(c.entries)).
			Msg("cleanup completed")
	}
}

// startMetricsUpdateRoutine periodically updates cache metrics
func (c *Cache) startMetricsUpdateRoutine() {
	// Initial update
	c.updateCacheMetrics()

	// Update every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.updateCacheMetrics()
		case <-c.done:
			return
		}
	}
}

// updateCacheMetrics updates cache size and count metrics
func (c *Cache) updateCacheMetrics() {
	c.lock.RLock()
	defer c.lock.RUnlock()

	// Update count
	archiveCacheCount.Set(float64(len(c.entries)))

	// Calculate total size
	var totalSize int64
	for _, entry := range c.entries {
		size, err := calculateDirSize(entry.extractedPath)
		if err != nil {
			log.Warn().Err(err).Str("dir", entry.extractedPath).Msg("failed to calculate archive size")
			continue
		}
		totalSize += size
	}

	archiveCacheSizeBytes.Set(float64(totalSize))
}

// Helper functions

// calculateDirSize calculates the total size of a directory in bytes
func calculateDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
