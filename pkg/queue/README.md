# Queue Package

This package implements a queue-based system for processing PR check requests with per-repository sequential processing and cross-repository parallelism.

## Overview

The queue system ensures that PR checks for the same repository are processed sequentially (to avoid git conflicts and cache contention), while PR checks for different repositories can be processed in parallel.

## Architecture

```
                    Webhook Arrives
                         |
                         v
                  ┌──────────────┐
                  │ QueueManager │
                  │   (Global)   │
                  └──────┬───────┘
                         |
              Lookup or Create Queue
                         |
        ┌────────────────┼────────────────┐
        v                v                v
   ┌─────────┐      ┌─────────┐     ┌─────────┐
   │ Queue A │      │ Queue B │     │ Queue C │
   │ Repo A  │      │ Repo B  │     │ Repo C  │
   └────┬────┘      └────┬────┘     └────┬────┘
        │                │               │
   Buffered Chan    Buffered Chan   Buffered Chan
   [PR3, PR2]       [PR5]           [PR7]
        │                │               │
        v                v               v
   ┌─────────┐      ┌─────────┐     ┌─────────┐
   │Worker A │      │Worker B │     │Worker C │
   │(1 goro) │      │(1 goro) │     │(1 goro) │
   └─────────┘      └─────────┘     └─────────┘
   Sequential       Sequential      Sequential
   Processing       Processing      Processing
```

## Components

### QueueManager

The global queue manager that:
- Maintains a map of `repoURL → RepoQueue`
- Creates new queues on-demand when a PR for a new repo arrives
- Routes PR check requests to the appropriate queue
- Manages graceful shutdown of all queues

**Key fields:**
```go
type QueueManager struct {
    queues      map[string]*RepoQueue  // One queue per repo
    mu          sync.RWMutex           // Protects queues map
    queueSize   int                    // Max buffered items per queue
    processFunc ProcessFunc            // Function to process each request
}
```

### RepoQueue

A dedicated queue for a single repository that:
- Uses a buffered channel to queue PR check requests
- Runs a single worker goroutine for sequential processing
- Tracks metrics (queued time, processed count)

**Key fields:**
```go
type RepoQueue struct {
    repoURL     string                 // Repository URL
    queue       chan *CheckRequest     // Buffered channel (default: 100)
    done        chan struct{}          // Shutdown signal
    wg          sync.WaitGroup         // Wait for worker completion
    processFunc ProcessFunc            // Processing function
    processed   int                    // Total processed count
    queuedAt    time.Time              // When first request was queued
}
```

### CheckRequest

Represents a single PR check request:
```go
type CheckRequest struct {
    PullRequest vcs.PullRequest           // PR metadata
    Container   container.Container       // App container with dependencies
    Processors  []checks.ProcessorEntry   // Check processors to run
    Timestamp   time.Time                 // When enqueued
}
```

## How It Works

### 1. Request Arrives

When a webhook arrives for a PR check:

```go
queueManager.Enqueue(ctx, EnqueueParams{
    PullRequest: pr,
    Container:   ctr,
    Processors:  processors,
})
```

### 2. Queue Lookup/Creation

The QueueManager:
- Canonicalizes the repo URL to create a key
- Checks if a queue exists for this repo
- If not, creates a new `RepoQueue` and starts its worker goroutine
- Enqueues the request (non-blocking with immediate failure if queue is full)

### 3. Worker Processing

Each `RepoQueue` runs a single worker goroutine:

```go
func (rq *RepoQueue) startWorker() {
    for {
        select {
        case request := <-rq.queue:
            rq.processRequest(request)  // Sequential processing
        case <-rq.done:
            return  // Graceful shutdown
        }
    }
}
```

The worker:
- Pulls requests from the channel one at a time
- Processes each request sequentially
- Records metrics (duration, success/failure)
- Continues until shutdown signal

### 4. Sequential Processing Per Repo

For a given repository:
```
PR #5 arrives → Enqueued
PR #6 arrives → Enqueued (waits in channel)
PR #7 arrives → Enqueued (waits in channel)

Worker: Process PR #5 (10s)
        Process PR #6 (8s)
        Process PR #7 (12s)
```

### 5. Parallel Processing Across Repos

Different repositories process in parallel:
```
Time 0s:
  Repo A: PR #5 starts processing
  Repo B: PR #3 starts processing  (parallel!)
  Repo C: PR #8 starts processing  (parallel!)

Time 10s:
  Repo A: PR #5 completes, PR #6 starts
  Repo B: PR #3 still processing
  Repo C: PR #8 completes, idle
```

## Configuration

```go
Config{
    QueueSize: 100,  // Max buffered requests per repo
}
```

- **Default queue size**: 100 requests per repository
- **Configured via**: `--repo-worker-max-queue-size` flag or `KUBECHECKS_MAX_REPO_WORKER_QUEUE_SIZE` env var

## Graceful Shutdown

When shutdown is initiated:

1. **Stop accepting new requests**: QueueManager closes all queue channels
2. **Drain in-flight work**: Workers complete current request
3. **Notify about dropped items**: Any remaining queued items trigger PR comments
4. **Wait for workers**: Use sync.WaitGroup to wait for all workers to finish

```go
func (qm *QueueManager) Shutdown(ctx context.Context) error {
    // Close all queues
    for _, queue := range qm.queues {
        close(queue.done)
    }

    // Wait for all workers (with timeout)
    done := make(chan struct{})
    go func() {
        for _, queue := range qm.queues {
            queue.wg.Wait()
        }
        close(done)
    }()

    select {
    case <-done:
        return nil  // Clean shutdown
    case <-ctx.Done():
        return ctx.Err()  // Timeout
    }
}
```

### Dropped Request Handling

If requests remain in queue during shutdown:
- Deduplicates by PR CheckID (to avoid rate limiting)
- Posts a single comment per unique PR
- Message: "⚠️ Kubechecks is shutting down. This check request was dropped. Please re-trigger by commenting `kubechecks replan`."

## Advantages Over Mutex-Based Approach

| Aspect | Mutex Locks | Queue System |
|--------|-------------|--------------|
| **Complexity** | High (lock/unlock, defer, cleanup) | Low (just enqueue) |
| **Error Prone** | Yes (forgot unlock, panic in cleanup) | No (channel auto-cleanup) |
| **Debugging** | Hard (deadlocks, race conditions) | Easy (visible queue state) |
| **Ordering** | Non-deterministic | FIFO guaranteed |
| **Backpressure** | N/A (unlimited blocking) | Built-in (buffer size) |
| **Monitoring** | Need custom metrics | `len(chan)` built-in |
| **Shutdown** | Complex (track locks) | Simple (close channel) |

## Metrics

All metrics are exposed at `/metrics` endpoint in Prometheus format.

### Queue Size Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `kubechecks_queue_repo_worker_size{repo="..."}` | Gauge | Current number of items in queue for specific repository |
| `kubechecks_queue_repo_worker_size_total` | Gauge | Total number of items across all queues |
| `kubechecks_queue_repo_worker_count` | Gauge | Number of active repository queues |

### Request Processing Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `kubechecks_queue_repo_worker_requests_total` | Counter | Total number of requests enqueued |
| `kubechecks_queue_repo_worker_requests_processed_total` | Counter | Total number of requests successfully processed |
| `kubechecks_queue_repo_worker_requests_failed_total` | Counter | Total number of requests that failed (panics) |
| `kubechecks_queue_repo_worker_processing_duration_seconds` | Histogram | Time taken to process each request |

**Histogram buckets**: 1, 5, 10, 30, 60, 120, 300, 600 seconds

## Example Metrics Queries

### Queue Health

```promql
# Average queue size per repo
avg(kubechecks_queue_repo_worker_size)

# Maximum queue size across all repos
max(kubechecks_queue_repo_worker_size)

# Total backlog
kubechecks_queue_repo_worker_size_total

# Number of active queues
kubechecks_queue_repo_worker_count
```

### Processing Rate

```promql
# Requests processed per second
rate(kubechecks_queue_repo_worker_requests_processed_total[5m])

# Request failure rate
rate(kubechecks_queue_repo_worker_requests_failed_total[5m]) /
rate(kubechecks_queue_repo_worker_requests_total[5m])

# Average processing time
rate(kubechecks_queue_repo_worker_processing_duration_seconds_sum[5m]) /
rate(kubechecks_queue_repo_worker_processing_duration_seconds_count[5m])
```

### Alerts

```yaml
# Queue backing up
- alert: RepoWorkerQueueBacklog
  expr: kubechecks_queue_repo_worker_size_total > 50
  for: 5m
  annotations:
    summary: "Repo worker queue has {{ $value }} items backlogged"

# Processing too slow
- alert: RepoWorkerProcessingSlow
  expr: |
    rate(kubechecks_queue_repo_worker_processing_duration_seconds_sum[5m]) /
    rate(kubechecks_queue_repo_worker_processing_duration_seconds_count[5m]) > 60
  for: 10m
  annotations:
    summary: "Repo worker processing averaging {{ $value }}s per request"

# High failure rate
- alert: RepoWorkerHighFailureRate
  expr: |
    rate(kubechecks_queue_repo_worker_requests_failed_total[5m]) /
    rate(kubechecks_queue_repo_worker_requests_total[5m]) > 0.1
  for: 5m
  annotations:
    summary: "Repo worker failure rate at {{ $value | humanizePercentage }}"
```

## Best Practices

### 1. Queue Size Configuration

- **Default (100)**: Good for most workloads
- **Increase**: If you have bursty PR traffic and want more buffering
- **Decrease**: If you want faster feedback about capacity issues

### 2. Monitoring

Monitor these key metrics:
- `kubechecks_queue_repo_worker_size_total` - Backlog indicator
- Processing duration p95/p99 - Performance indicator
- Failure rate - Health indicator

### 3. Capacity Planning

If queues consistently fill up:
- Scale horizontally (more kubechecks pods)
- Optimize check processing time
- Consider increasing queue size temporarily

### 4. Troubleshooting

**Symptom**: Requests timing out
- Check: `kubechecks_queue_repo_worker_size` - Is queue full?
- Check: Processing duration - Are checks taking too long?

**Symptom**: PRs not getting processed
- Check: `kubechecks_queue_repo_worker_count` - Are queues being created?
- Check logs for queue creation messages

**Symptom**: High failure rate
- Check: `kubechecks_queue_repo_worker_requests_failed_total`
- Look for panic messages in logs

## Archive Mode

Starting with the archive mode feature, kubechecks can use VCS API archive downloads instead of git clone/merge operations. This significantly improves performance and reduces disk I/O.

### How Archive Mode Works

When archive mode is enabled (`--archive-mode=true`):

1. **PR arrives** → Queue system enqueues request (same as before)
2. **Worker picks up request** → Uses `pkg/archive` instead of `pkg/git`
3. **Archive download**:
   - **GitHub**: Downloads merge commit SHA archive via GitHub API
   - **GitLab**: Downloads preview merge via `refs/merge-requests/<iid>/merge` ref
4. **Cache hit optimization**: Archives are cached by merge commit SHA
5. **Processing**: Checks run against extracted archive (same as git checkout)

### Archive Mode vs Git Mode

| Aspect | Git Mode (Legacy) | Archive Mode (New) |
|--------|-------------------|-------------------|
| **Operation** | `git clone` + `git merge` | HTTP download + extract |
| **Speed** | ~10-30s for clone | ~2-5s for download |
| **Disk I/O** | High (full .git dir) | Low (no .git dir) |
| **Cache Key** | Clone URL + Branch | Merge commit SHA |
| **Conflicts** | Detected via git merge | Detected via VCS API |
| **Changed Files** | `git diff` | VCS API (PR files endpoint) |
| **Network** | Git protocol | HTTPS REST API |
| **Auth** | Git credentials | VCS API token |

### Configuration

```bash
# Enable archive mode
--archive-mode=true

# Archive cache directory
--archive-cache-dir=/tmp/kubechecks/archives

# Archive cache TTL
--archive-cache-ttl=1h
```

Environment variables:
```bash
KUBECHECKS_ARCHIVE_MODE=true
KUBECHECKS_ARCHIVE_CACHE_DIR=/tmp/kubechecks/archives
KUBECHECKS_ARCHIVE_CACHE_TTL=1h
```

### Archive Cache Behavior

Archives are cached by merge commit SHA (globally unique):
- **Cache hit**: Reuse extracted archive (instant)
- **Cache miss**: Download and extract (2-5s)
- **TTL-based cleanup**: Removes stale archives every 15 minutes
- **Reference counting**: Archives in use are never cleaned up

### VCS-Specific Implementation

#### GitHub
- **Archive URL**: `https://api.github.com/repos/{owner}/{repo}/zipball/{sha}`
- **Auth Header**: `Authorization: Bearer <token>`
- **Merge SHA**: Uses GitHub's `merge_commit_sha` from PR API
- **Changed Files**: Uses `GET /repos/{owner}/{repo}/pulls/{number}/files`

#### GitLab
- **Archive URL**: `https://gitlab.com/api/v4/projects/{project_encoded}/repository/archive.zip?sha=refs/merge-requests/{iid}/merge`
- **Auth Header**: `PRIVATE-TOKEN: <token>`
- **Merge Ref**: Uses `refs/merge-requests/<iid>/merge` (preview merge state)
- **Changed Files**: Uses `GET /projects/{id}/merge_requests/{iid}/diffs`

**Note**: GitLab's `merge_commit_sha` is null until MR is actually merged, so we use the special merge ref instead.

### Sequential Processing with Archive Mode

Archive mode maintains the same sequential processing guarantees:

```
Time 0s: PR #5 arrives → Download archive (3s)
Time 3s: PR #5 processing starts → Run checks (15s)
Time 18s: PR #5 completes, PR #6 starts → Download archive (cache hit = 0s)
Time 18s: PR #6 processing starts → Run checks (12s)
```

**Benefits of archive mode + queue system**:
- No git conflicts (archives are immutable snapshots)
- Faster cache hits (no git fetch needed)
- Better parallelism (no shared .git directories)

### Fallback Behavior

If archive mode fails (e.g., API rate limit, network issues), kubechecks will:
1. Log the error with details (HTTP status, response body)
2. Fail the check (no automatic fallback to git mode)
3. Post error message to PR

To handle failures, you can:
- Monitor archive download metrics
- Increase API rate limits if needed
- Re-trigger check by commenting `kubechecks replan`

### Metrics

Archive mode adds new metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `kubechecks_archive_cache_hits_total` | Counter | Archive cache hit count |
| `kubechecks_archive_cache_misses_total` | Counter | Archive cache miss count |
| `kubechecks_archive_cache_size` | Gauge | Number of cached archives |
| `kubechecks_archive_cache_size_bytes` | Gauge | Total disk space used by cache |
| `kubechecks_archive_download_duration_seconds` | Histogram | Time to download archive |
| `kubechecks_archive_extract_duration_seconds` | Histogram | Time to extract archive |

### Monitoring Archive Mode

```promql
# Cache hit rate
rate(kubechecks_archive_cache_hits_total[5m]) /
(rate(kubechecks_archive_cache_hits_total[5m]) + rate(kubechecks_archive_cache_misses_total[5m]))

# Average download time
rate(kubechecks_archive_download_duration_seconds_sum[5m]) /
rate(kubechecks_archive_download_duration_seconds_count[5m])

# Cache disk usage
kubechecks_archive_cache_size_bytes / (1024 * 1024 * 1024)  # GB
```

## Related Packages

- **`pkg/git`**: Repository cache and git operations (legacy mode)
- **`pkg/archive`**: Archive download and caching (archive mode)
- **`pkg/events`**: Check event processing
- **`pkg/checks`**: Individual check processors (diff, schema, policy, etc.)
- **`pkg/server`**: Webhook handlers that enqueue requests
- **`pkg/vcs`**: VCS client abstraction (GitHub, GitLab)
