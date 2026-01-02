package git

import (
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

var (
	// Clone operation metrics
	repoCloneTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_clone_total",
			Help:      "Total number of repository clone operations attempted",
		},
	)
	repoCloneSuccess = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_clone_success_total",
			Help:      "Number of successful repository clone operations",
		},
	)
	repoCloneFailed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_clone_failed_total",
			Help:      "Number of failed repository clone operations",
		},
	)
	repoCloneDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_clone_duration_seconds",
			Help:      "Time taken to clone repository (seconds)",
			Buckets:   []float64{1, 5, 10, 30, 60, 120, 300},
		},
	)

	// Fetch operation metrics
	repoFetchTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_fetch_total",
			Help:      "Total number of git fetch operations attempted",
		},
	)
	repoFetchSuccess = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_fetch_success_total",
			Help:      "Number of successful git fetch operations",
		},
	)
	repoFetchFailed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_fetch_failed_total",
			Help:      "Number of failed git fetch operations",
		},
	)
	repoFetchDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_fetch_duration_seconds",
			Help:      "Time taken to fetch repository updates (seconds)",
			Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30},
		},
	)

	// Cache state metrics
	repoCacheCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_cache_count",
			Help:      "Number of repositories currently cached on disk",
		},
	)
	repoCacheSizeBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_cache_size_bytes",
			Help:      "Total disk space used by cached repositories (bytes)",
		},
	)

	// Cache hit/miss metrics
	repoCacheHits = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_cache_hits_total",
			Help:      "Number of times a cached repository was reused",
		},
	)
	repoCacheMisses = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "git",
			Name:      "repo_cache_misses_total",
			Help:      "Number of times a repository needed to be cloned (cache miss)",
		},
	)
)

func init() {
	r := prometheus.DefaultRegisterer

	// Clone metrics
	r.MustRegister(repoCloneTotal)
	r.MustRegister(repoCloneSuccess)
	r.MustRegister(repoCloneFailed)
	r.MustRegister(repoCloneDuration)

	// Fetch metrics
	r.MustRegister(repoFetchTotal)
	r.MustRegister(repoFetchSuccess)
	r.MustRegister(repoFetchFailed)
	r.MustRegister(repoFetchDuration)

	// Cache metrics
	r.MustRegister(repoCacheCount)
	r.MustRegister(repoCacheSizeBytes)
	r.MustRegister(repoCacheHits)
	r.MustRegister(repoCacheMisses)
}

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

// updateCacheMetrics updates cache size and count metrics
func (rm *PersistentRepoManager) updateCacheMetrics() {
	rm.lock.RLock()
	defer rm.lock.RUnlock()

	// Update count
	repoCacheCount.Set(float64(len(rm.repos)))

	// Calculate total size
	var totalSize int64
	for _, pr := range rm.repos {
		size, err := calculateDirSize(pr.Directory)
		if err != nil {
			log.Warn().Err(err).Str("dir", pr.Directory).Msg("failed to calculate repo size")
			continue
		}
		totalSize += size
	}

	repoCacheSizeBytes.Set(float64(totalSize))
}

// startMetricsUpdateRoutine periodically updates cache metrics
func (rm *PersistentRepoManager) startMetricsUpdateRoutine() {
	// Initial update
	rm.updateCacheMetrics()

	// Update every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rm.updateCacheMetrics()
		case <-rm.done:
			return
		}
	}
}
