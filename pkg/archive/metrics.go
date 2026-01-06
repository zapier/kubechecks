package archive

import "github.com/prometheus/client_golang/prometheus"

var (
	// Download metrics
	archiveDownloadTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "download_total",
			Help:      "Total number of archive download attempts",
		},
	)
	archiveDownloadSuccess = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "download_success_total",
			Help:      "Number of successful archive downloads",
		},
	)
	archiveDownloadFailed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "download_failed_total",
			Help:      "Number of failed archive downloads",
		},
	)
	archiveDownloadDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "download_duration_seconds",
			Help:      "Time taken to download archive (seconds)",
			Buckets:   []float64{1, 5, 10, 30, 60, 120},
		},
	)
	archiveDownloadSizeBytes = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "download_size_bytes",
			Help:      "Size of downloaded archives in bytes",
			Buckets:   []float64{1e6, 10e6, 50e6, 100e6, 500e6, 1e9}, // 1MB to 1GB
		},
	)

	// Extraction metrics
	archiveExtractTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "extract_total",
			Help:      "Total number of archive extraction attempts",
		},
	)
	archiveExtractSuccess = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "extract_success_total",
			Help:      "Number of successful archive extractions",
		},
	)
	archiveExtractFailed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "extract_failed_total",
			Help:      "Number of failed archive extractions",
		},
	)
	archiveExtractDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "extract_duration_seconds",
			Help:      "Time taken to extract archive (seconds)",
			Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30},
		},
	)

	// Cache metrics
	archiveCacheHits = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "cache_hits_total",
			Help:      "Number of times a cached archive was reused",
		},
	)
	archiveCacheMisses = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "cache_misses_total",
			Help:      "Number of times an archive needed to be downloaded (cache miss)",
		},
	)
	archiveCacheCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "cache_count",
			Help:      "Number of archives currently cached",
		},
	)
	archiveCacheSizeBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "kubechecks",
			Subsystem: "archive",
			Name:      "cache_size_bytes",
			Help:      "Total disk space used by cached archives (bytes)",
		},
	)
)

func init() {
	r := prometheus.DefaultRegisterer

	// Download metrics
	r.MustRegister(archiveDownloadTotal)
	r.MustRegister(archiveDownloadSuccess)
	r.MustRegister(archiveDownloadFailed)
	r.MustRegister(archiveDownloadDuration)
	r.MustRegister(archiveDownloadSizeBytes)

	// Extraction metrics
	r.MustRegister(archiveExtractTotal)
	r.MustRegister(archiveExtractSuccess)
	r.MustRegister(archiveExtractFailed)
	r.MustRegister(archiveExtractDuration)

	// Cache metrics
	r.MustRegister(archiveCacheHits)
	r.MustRegister(archiveCacheMisses)
	r.MustRegister(archiveCacheCount)
	r.MustRegister(archiveCacheSizeBytes)
}
