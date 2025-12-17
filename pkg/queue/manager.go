package queue

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs"
)

// ProcessFunc is the function signature for processing a check request
type ProcessFunc func(ctx context.Context, pr vcs.PullRequest, ctr container.Container, processors []checks.ProcessorEntry) error

// CheckRequest represents a PR check request to be queued
type CheckRequest struct {
	PullRequest vcs.PullRequest
	Container   container.Container
	Processors  []checks.ProcessorEntry
	Timestamp   time.Time
}

// RepoQueue manages a queue of check requests for a single repository
type RepoQueue struct {
	repoURL     string
	queue       chan *CheckRequest
	done        chan struct{}
	wg          sync.WaitGroup
	mu          sync.Mutex
	queuedAt    time.Time
	processed   int
	processFunc ProcessFunc
}

// QueueManager manages all repository queues
type QueueManager struct {
	queues      map[string]*RepoQueue
	mu          sync.RWMutex
	queueSize   int
	processFunc ProcessFunc
}

// Config holds queue manager configuration
type Config struct {
	QueueSize int
}

// NewQueueManager creates a new queue manager
func NewQueueManager(cfg Config, processFunc ProcessFunc) *QueueManager {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 100 // Default buffer size
	}

	return &QueueManager{
		queues:      make(map[string]*RepoQueue),
		queueSize:   cfg.QueueSize,
		processFunc: processFunc,
	}
}

// EnqueueParams contains the parameters for enqueueing a check request
type EnqueueParams struct {
	PullRequest vcs.PullRequest
	Container   container.Container
	Processors  []checks.ProcessorEntry
}

// Enqueue adds a check request to the appropriate repository queue
// Returns error if queue is full (non-blocking)
func (qm *QueueManager) Enqueue(ctx context.Context, params EnqueueParams) error {
	// extract the url from pullrequest, use it as the key
	repoURL, err := pkg.Canonicalize(params.PullRequest.CloneURL)
	if err != nil {
		return fmt.Errorf("failed to canonicalize repo URL: %w", err)
	}
	repoKey := fmt.Sprintf("%s/%s", repoURL.Host, repoURL.Path)

	// Get or create queue for this repo using double-checked locking for better concurrency
	// Fast path: read lock to check if queue exists
	qm.mu.RLock()
	queue, exists := qm.queues[repoKey]
	qm.mu.RUnlock()

	if !exists {
		// Slow path: write lock to create queue
		qm.mu.Lock()
		// Double-check in case another goroutine created it while we were waiting for the lock
		queue, exists = qm.queues[repoKey]
		if !exists {
			queue = &RepoQueue{
				repoURL:     params.PullRequest.CloneURL,
				queue:       make(chan *CheckRequest, qm.queueSize),
				done:        make(chan struct{}),
				queuedAt:    time.Now(),
				processFunc: qm.processFunc,
			}
			qm.queues[repoKey] = queue

			// Start dedicated worker goroutine for this repo
			queue.wg.Add(1)
			go queue.startWorker()

			log.Info().
				Str("repo", params.PullRequest.CloneURL).
				Int("queue_size", qm.queueSize).
				Msg("created new queue and started worker")
		}
		qm.mu.Unlock()
	}

	// Create check request
	request := &CheckRequest{
		PullRequest: params.PullRequest,
		Container:   params.Container,
		Processors:  params.Processors,
		Timestamp:   time.Now(),
	}

	// Try to enqueue (non-blocking)
	select {
	case queue.queue <- request:
		repoWorkerRequestsTotal.Inc()
		repoWorkerQueueSize.WithLabelValues(repoKey).Set(float64(len(queue.queue)))
		qm.updateTotalQueueMetrics()
		log.Info().
			Str("repo", params.PullRequest.CloneURL).
			Int("check_id", params.PullRequest.CheckID).
			Int("queue_length", len(queue.queue)).
			Msg("enqueued PR check request")
		return nil
	default:
		// Queue is full, return error immediately without blocking
		log.Warn().
			Str("repo", params.PullRequest.CloneURL).
			Int("check_id", params.PullRequest.CheckID).
			Int("queue_size", qm.queueSize).
			Msg("queue full, rejecting request")
		return fmt.Errorf("queue full for repo %s (queue size: %d)", params.PullRequest.CloneURL, qm.queueSize)
	}
}

// startWorker processes check requests sequentially for this repository
func (rq *RepoQueue) startWorker() {
	defer rq.wg.Done()

	log.Info().
		Str("repo", rq.repoURL).
		Msg("worker started, waiting for requests")

	for {
		select {
		case request := <-rq.queue:
			rq.processRequest(request)
		case <-rq.done:
			// Drain remaining items and notify affected PRs
			remaining := len(rq.queue)
			if remaining > 0 {
				log.Warn().
					Str("repo", rq.repoURL).
					Int("dropped_count", remaining).
					Msg("shutdown initiated, draining queue and notifying PRs")

				rq.notifyDroppedRequests()
			}

			log.Info().
				Str("repo", rq.repoURL).
				Int("processed", rq.processed).
				Msg("worker shutting down")
			return
		}
	}
}

// notifyDroppedRequests notifies PRs about dropped items during shutdown
func (rq *RepoQueue) notifyDroppedRequests() {
	// Deduplicate by PR to avoid rate limiting (multiple items for same PR = 1 comment)
	prMap := make(map[int]*CheckRequest) // key: CheckID

	// Drain queue and deduplicate
	drained := 0
	for {
		select {
		case request := <-rq.queue:
			prMap[request.PullRequest.CheckID] = request
			drained++
		default:
			// Queue empty
			goto notify
		}
	}

notify:
	if len(prMap) == 0 {
		return
	}

	log.Info().
		Str("repo", rq.repoURL).
		Int("drained", drained).
		Int("unique_prs", len(prMap)).
		Msg("notifying PRs about dropped items")

	// Notify each unique PR (with timeout to avoid blocking shutdown)
	notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, request := range prMap {
		select {
		case <-notifyCtx.Done():
			log.Warn().Msg("timeout reached while notifying PRs, stopping")
			return
		default:
			message := fmt.Sprintf("⚠️ Kubechecks is shutting down. This check request was dropped.\n\n"+
				"Please re-trigger by commenting `%s`.",
				request.Container.Config.ReplanCommentMessage)

			if _, err := request.Container.VcsClient.PostMessage(notifyCtx, request.PullRequest, message); err != nil {
				log.Error().
					Err(err).
					Caller().
					Str("repo", request.PullRequest.CloneURL).
					Int("check_id", request.PullRequest.CheckID).
					Msg("failed to post shutdown notification")
			} else {
				log.Debug().
					Caller().
					Str("repo", request.PullRequest.CloneURL).
					Int("check_id", request.PullRequest.CheckID).
					Msg("posted shutdown notification")
			}
		}
	}
}

// processRequest handles a single check request
func (rq *RepoQueue) processRequest(request *CheckRequest) {
	start := time.Now()
	timer := prometheus.NewTimer(repoWorkerProcessingDuration)
	defer timer.ObserveDuration()

	log.Info().
		Str("repo", request.PullRequest.CloneURL).
		Int("check_id", request.PullRequest.CheckID).
		Dur("queued_for", time.Since(request.Timestamp)).
		Msg("worker processing request")

	err := rq.processFunc(
		context.Background(),
		request.PullRequest,
		request.Container,
		request.Processors,
	)
	if err != nil {
		repoWorkerRequestsFailed.Inc()
		log.Error().
			Str("repo", request.PullRequest.CloneURL).
			Int("check_id", request.PullRequest.CheckID).
			Msg("worker recovered from panic, continuing to process next request")
		return
	}

	// Update metrics
	rq.mu.Lock()
	rq.processed++
	processed := rq.processed
	rq.mu.Unlock()

	log.Info().
		Str("repo", request.PullRequest.CloneURL).
		Int("check_id", request.PullRequest.CheckID).
		Dur("duration", time.Since(start)).
		Int("total_processed", processed).
		Msg("worker completed request")
}

// Shutdown gracefully shuts down all queues and workers
func (qm *QueueManager) Shutdown(ctx context.Context) error {
	log.Info().Msg("shutting down queue manager")

	qm.mu.Lock()
	queues := make([]*RepoQueue, 0, len(qm.queues))
	for _, queue := range qm.queues {
		queues = append(queues, queue)
	}
	qm.mu.Unlock()

	// Signal all workers to stop
	for _, queue := range queues {
		close(queue.done)
	}

	// Wait for all workers to finish with timeout
	done := make(chan struct{})
	go func() {
		for _, queue := range queues {
			queue.wg.Wait()
		}
		close(done)
	}()

	select {
	case <-done:
		log.Info().Msg("all queue workers shutdown successfully")
		return nil
	case <-ctx.Done():
		log.Warn().Msg("queue shutdown timed out")
		return ctx.Err()
	}
}

// GetStats returns statistics about all queues
func (qm *QueueManager) GetStats() map[string]interface{} {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	stats := make(map[string]interface{})
	stats["total_queues"] = len(qm.queues)

	queues := make([]map[string]interface{}, 0, len(qm.queues))
	for _, queue := range qm.queues {
		queue.mu.Lock()
		queueStats := map[string]interface{}{
			"repo_url":     queue.repoURL,
			"queue_length": len(queue.queue),
			"queue_cap":    cap(queue.queue),
			"processed":    queue.processed,
			"queued_since": time.Since(queue.queuedAt).String(),
		}
		queue.mu.Unlock()
		queues = append(queues, queueStats)
	}
	stats["queues"] = queues

	return stats
}

// updateTotalQueueMetrics updates aggregate queue metrics
func (qm *QueueManager) updateTotalQueueMetrics() {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	var totalSize int
	for _, queue := range qm.queues {
		totalSize += len(queue.queue)
	}

	repoWorkerQueueTotal.Set(float64(totalSize))
	repoWorkerQueueCount.Set(float64(len(qm.queues)))
}
