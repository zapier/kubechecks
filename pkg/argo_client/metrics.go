package argo_client

import "github.com/prometheus/client_golang/prometheus"

var (
	commonLabels = []string{
		"application",
	}
	getManifestsSuccess = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Name:      "get_manifests_success",
			Help:      "Count of all attempts to get application manifests that succeeded",
		},
		commonLabels,
	)
	getManifestsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Name:      "get_manifests_failure",
			Help:      "Count of all attempts to get application manifests that failed",
		},
		commonLabels,
	)

	buckets = []float64{1, 30, 60, 300}

	getManifestsDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "kubechecks",
		Name:      "get_manifests_duration_seconds",
		Help:      "Histogram of response time for application manifest generation",
		Buckets:   buckets,
	},
		commonLabels,
	)
)

func init() {
	r := prometheus.DefaultRegisterer

	r.MustRegister(getManifestsFailed)
	r.MustRegister(getManifestsSuccess)
	r.MustRegister(getManifestsDuration)
}
