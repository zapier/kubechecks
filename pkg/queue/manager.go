package queue

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs"
)

// AIReviewChecker is the interface for the AI review checker (mirrors events.AIReviewChecker).
type AIReviewChecker interface {
	Check(ctx context.Context, request checks.Request) (vcs.AIReviewResult, error)
	AggregateReviews(ctx context.Context, appReviews map[string]string) (string, error)
}

// ProcessFunc is the function signature for processing a check request
type ProcessFunc func(ctx context.Context, pr vcs.PullRequest, ctr container.Container, processors []checks.ProcessorEntry, aiReviewChecker AIReviewChecker) error

// CheckRequest represents a PR check request to be queued
type CheckRequest struct {
	PullRequest     vcs.PullRequest
	Container       container.Container
	Processors      []checks.ProcessorEntry
	AIReviewChecker AIReviewChecker
	Timestamp       time.Time

	// queuedStatusPosted is closed once the queued (running) commit status call
	// has returned. The worker waits on it so the final status always lands last.
	queuedStatusPosted chan struct{}
	queuedStatusCtx    context.Context
	cancelQueuedStatus context.CancelFunc
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

	// stopCtx is set before done is closed and bounds shutdown cleanup.
	stopCtx context.Context
}

// QueueManager manages all repository queues
type QueueManager struct {
	queues      map[string]*RepoQueue
	mu          sync.RWMutex
	queueSize   int
	processFunc ProcessFunc
}

const (
	// queuedStatusTimeout bounds the running status call made at enqueue time,
	// including any wait for the VCS pipeline it attaches to.
	queuedStatusTimeout = 20 * time.Second

	// droppedRequestTimeout bounds each VCS call made for a request dropped on shutdown.
	droppedRequestTimeout = 3 * time.Second

	// droppedRequestConcurrency caps parallel VCS calls during shutdown cleanup.
	droppedRequestConcurrency = 10
)

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
	PullRequest     vcs.PullRequest
	Container       container.Container
	Processors      []checks.ProcessorEntry
	AIReviewChecker AIReviewChecker
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
	request := newCheckRequest(params)

	// Try to enqueue (non-blocking)
	select {
	case queue.queue <- request:
		// Report the check as running while it waits in the queue, so the VCS
		// doesn't treat the PR/MR as passing before kubechecks has reported.
		// Sent asynchronously so the webhook returns quickly.
		go postQueuedStatus(request)

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
		request.cancelQueuedStatus()
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

				rq.notifyDroppedRequests(rq.stopCtx)
			}

			log.Info().
				Str("repo", rq.repoURL).
				Int("processed", rq.processed).
				Msg("worker shutting down")
			return
		}
	}
}

// newCheckRequest builds a queued check request with its queued-status bookkeeping.
func newCheckRequest(params EnqueueParams) *CheckRequest {
	ctx, cancel := context.WithTimeout(context.Background(), queuedStatusTimeout)
	return &CheckRequest{
		PullRequest:     params.PullRequest,
		Container:       params.Container,
		Processors:      params.Processors,
		AIReviewChecker: params.AIReviewChecker,
		Timestamp:       time.Now(),

		queuedStatusPosted: make(chan struct{}),
		queuedStatusCtx:    ctx,
		cancelQueuedStatus: cancel,
	}
}

// postQueuedStatus marks the commit as running and then closes queuedStatusPosted.
func postQueuedStatus(request *CheckRequest) {
	defer close(request.queuedStatusPosted)
	defer request.cancelQueuedStatus()

	if err := request.Container.VcsClient.CommitStatus(request.queuedStatusCtx, request.PullRequest, pkg.StateRunning); err != nil {
		log.Warn().
			Err(err).
			Str("repo", request.PullRequest.CloneURL).
			Int("check_id", request.PullRequest.CheckID).
			Msg("failed to set queued commit status")
	}
}

// notifyDroppedRequests notifies PRs about dropped items during shutdown. ctx
// is the shutdown context; work left when it expires is abandoned.
func (rq *RepoQueue) notifyDroppedRequests(ctx context.Context) {
	// This worker is the queue's only consumer, so a non-empty queue can't block.
	var dropped []*CheckRequest
	for len(rq.queue) > 0 {
		dropped = append(dropped, <-rq.queue)
	}

	if len(dropped) == 0 {
		return
	}

	log.Info().
		Str("repo", rq.repoURL).
		Int("drained", len(dropped)).
		Msg("notifying PRs about dropped items")

	// Stop any running status still in flight, and wait for it to return, so it
	// can't land after the error status set below.
	for _, request := range dropped {
		request.cancelQueuedStatus()
	}
	for _, request := range dropped {
		<-request.queuedStatusPosted
	}

	// Every dropped commit gets an error status, so none is left running. These
	// all go out before any comment, since a stuck status is what blocks merges.
	var commits, prs []*CheckRequest
	finalized := make(map[string]bool)
	commented := make(map[int]bool)
	for _, request := range dropped {
		pr := request.PullRequest
		if commitKey := pr.FullName + "@" + pr.SHA; !finalized[commitKey] {
			finalized[commitKey] = true
			commits = append(commits, request)
		}
		if !commented[pr.CheckID] {
			commented[pr.CheckID] = true
			prs = append(prs, request)
		}
	}

	forEachDropped(ctx, commits, func(callCtx context.Context, request *CheckRequest) {
		if err := request.Container.VcsClient.CommitStatus(callCtx, request.PullRequest, pkg.StateError); err != nil {
			droppedLogger(request).Error().Err(err).Caller().Msg("failed to set commit status for dropped request")
		}
	})

	// The shutdown comment is posted once per PR, even if its status update failed.
	forEachDropped(ctx, prs, func(callCtx context.Context, request *CheckRequest) {
		message := fmt.Sprintf("⚠️ Kubechecks is shutting down. This check request was dropped.\n\n"+
			"Please re-trigger by commenting `%s`.",
			request.Container.Config.ReplanCommentMessage)

		if _, err := request.Container.VcsClient.PostMessage(callCtx, request.PullRequest, message); err != nil {
			droppedLogger(request).Error().Err(err).Caller().Msg("failed to post shutdown notification")
		} else {
			droppedLogger(request).Debug().Caller().Msg("posted shutdown notification")
		}
	})
}

// forEachDropped calls fn for every request, at most droppedRequestConcurrency
// at a time, each with its own droppedRequestTimeout derived from ctx.
func forEachDropped(ctx context.Context, requests []*CheckRequest, fn func(context.Context, *CheckRequest)) {
	var g errgroup.Group
	g.SetLimit(droppedRequestConcurrency)
	for _, request := range requests {
		g.Go(func() error {
			callCtx, cancel := context.WithTimeout(ctx, droppedRequestTimeout)
			defer cancel()
			fn(callCtx, request)
			return nil
		})
	}
	_ = g.Wait()
}

func droppedLogger(request *CheckRequest) *zerolog.Logger {
	pr := request.PullRequest
	logger := log.With().Str("repo", pr.CloneURL).Int("check_id", pr.CheckID).Str("sha", pr.SHA).Logger()
	return &logger
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

	// Bounded by queuedStatusTimeout.
	<-request.queuedStatusPosted

	err := rq.processFunc(
		context.Background(),
		request.PullRequest,
		request.Container,
		request.Processors,
		request.AIReviewChecker,
	)
	if err != nil {
		repoWorkerRequestsFailed.Inc()
		log.Error().
			Str("repo", request.PullRequest.CloneURL).
			Int("check_id", request.PullRequest.CheckID).
			Err(err).
			Msg("worker failed to process request, continuing to next request")
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
		queue.stopCtx = ctx
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
