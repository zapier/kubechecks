package queue

import "github.com/prometheus/client_golang/prometheus"

var (
	// Queue size metrics
	repoWorkerQueueSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "kubechecks",
			Subsystem: "queue",
			Name:      "repo_worker_size",
			Help:      "Current number of items in repo worker queue by repository",
		},
		[]string{"repo"},
	)

	repoWorkerQueueTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "kubechecks",
			Subsystem: "queue",
			Name:      "repo_worker_size_total",
			Help:      "Total number of items across all repo worker queues",
		},
	)

	repoWorkerQueueCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "kubechecks",
			Subsystem: "queue",
			Name:      "repo_worker_count",
			Help:      "Number of active repo worker queues",
		},
	)

	// Request processing metrics
	repoWorkerRequestsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "queue",
			Name:      "repo_worker_requests_total",
			Help:      "Total number of requests enqueued",
		},
	)

	repoWorkerRequestsProcessed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "queue",
			Name:      "repo_worker_requests_processed_total",
			Help:      "Total number of requests successfully processed",
		},
	)

	repoWorkerRequestsFailed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Subsystem: "queue",
			Name:      "repo_worker_requests_failed_total",
			Help:      "Total number of requests that failed processing",
		},
	)

	repoWorkerProcessingDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "kubechecks",
			Subsystem: "queue",
			Name:      "repo_worker_processing_duration_seconds",
			Help:      "Time taken to process a check request (seconds)",
			Buckets:   []float64{1, 5, 10, 30, 60, 120, 300, 600},
		},
	)
)

func init() {
	r := prometheus.DefaultRegisterer

	// Queue metrics
	r.MustRegister(repoWorkerQueueSize)
	r.MustRegister(repoWorkerQueueTotal)
	r.MustRegister(repoWorkerQueueCount)

	// Request metrics
	r.MustRegister(repoWorkerRequestsTotal)
	r.MustRegister(repoWorkerRequestsProcessed)
	r.MustRegister(repoWorkerRequestsFailed)
	r.MustRegister(repoWorkerProcessingDuration)
}
