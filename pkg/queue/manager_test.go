package queue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	vcsmocks "github.com/zapier/kubechecks/mocks/vcs/mocks"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func testPullRequest(checkID int, sha string) vcs.PullRequest {
	return vcs.PullRequest{
		CloneURL: "https://gitlab.com/example/repo.git",
		FullName: "example/repo",
		CheckID:  checkID,
		SHA:      sha,
	}
}

func checkIDIs(id int) interface{} {
	return mock.MatchedBy(func(pr vcs.PullRequest) bool { return pr.CheckID == id })
}

// queuedRequest returns a request whose running status has already been posted.
func queuedRequest(ctr container.Container, pr vcs.PullRequest) *CheckRequest {
	request := newCheckRequest(EnqueueParams{PullRequest: pr, Container: ctr})
	request.cancelQueuedStatus()
	close(request.queuedStatusPosted)
	return request
}

func TestEnqueue_SetsRunningStatusBeforeProcessing(t *testing.T) {
	vcsClient := vcsmocks.NewMockClient(t)
	ctr := container.Container{VcsClient: vcsClient}
	pr := testPullRequest(1, "aaa")

	releaseStatus := make(chan struct{})
	vcsClient.EXPECT().
		CommitStatus(mock.Anything, pr, pkg.StateRunning).
		RunAndReturn(func(context.Context, vcs.PullRequest, pkg.CommitState) error {
			<-releaseStatus
			return nil
		}).
		Once()

	processed := make(chan struct{})
	qm := NewQueueManager(Config{QueueSize: 10}, func(context.Context, vcs.PullRequest, container.Container, []checks.ProcessorEntry, AIReviewChecker) error {
		close(processed)
		return nil
	})

	require.NoError(t, qm.Enqueue(context.Background(), EnqueueParams{PullRequest: pr, Container: ctr}))

	select {
	case <-processed:
		t.Fatal("request was processed before the running status was set")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseStatus)

	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("request was not processed after the running status was set")
	}

	require.NoError(t, qm.Shutdown(context.Background()))
}

func TestEnqueue_QueueFullDoesNotSetStatus(t *testing.T) {
	vcsClient := vcsmocks.NewMockClient(t)
	ctr := container.Container{VcsClient: vcsClient}

	// Only the two accepted requests get a running status.
	vcsClient.EXPECT().
		CommitStatus(mock.Anything, mock.Anything, pkg.StateRunning).
		Return(nil).
		Times(2)

	started := make(chan struct{}, 2)
	unblock := make(chan struct{})
	qm := NewQueueManager(Config{QueueSize: 1}, func(context.Context, vcs.PullRequest, container.Container, []checks.ProcessorEntry, AIReviewChecker) error {
		started <- struct{}{}
		<-unblock
		return nil
	})

	// The first request is picked up by the worker and blocks, the second fills the buffer.
	require.NoError(t, qm.Enqueue(context.Background(), EnqueueParams{PullRequest: testPullRequest(1, "aaa"), Container: ctr}))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request was not picked up")
	}
	require.NoError(t, qm.Enqueue(context.Background(), EnqueueParams{PullRequest: testPullRequest(2, "bbb"), Container: ctr}))

	assert.Error(t, qm.Enqueue(context.Background(), EnqueueParams{PullRequest: testPullRequest(3, "ccc"), Container: ctr}))

	close(unblock)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second request was not processed")
	}

	require.NoError(t, qm.Shutdown(context.Background()))
}

func TestNotifyDroppedRequests_FinalizesEveryCommitAndCommentsOncePerPR(t *testing.T) {
	vcsClient := vcsmocks.NewMockClient(t)
	ctr := container.Container{VcsClient: vcsClient}
	first, second, other := testPullRequest(1, "aaa"), testPullRequest(1, "bbb"), testPullRequest(2, "ccc")

	for _, pr := range []vcs.PullRequest{first, second, other} {
		vcsClient.EXPECT().CommitStatus(mock.Anything, pr, pkg.StateError).Return(nil).Once()
	}
	vcsClient.EXPECT().PostMessage(mock.Anything, checkIDIs(1), mock.Anything).Return(nil, nil).Once()
	vcsClient.EXPECT().PostMessage(mock.Anything, checkIDIs(2), mock.Anything).Return(nil, nil).Once()

	rq := &RepoQueue{repoURL: first.CloneURL, queue: make(chan *CheckRequest, 3)}
	for _, pr := range []vcs.PullRequest{first, second, other} {
		rq.queue <- queuedRequest(ctr, pr)
	}

	rq.notifyDroppedRequests(context.Background())
}

func TestNotifyDroppedRequests_CancelsRunningStatusBeforeError(t *testing.T) {
	vcsClient := vcsmocks.NewMockClient(t)
	ctr := container.Container{VcsClient: vcsClient}
	pr := testPullRequest(1, "aaa")

	var mu sync.Mutex
	var order []pkg.CommitState
	record := func(state pkg.CommitState) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, state)
	}

	inFlight := make(chan struct{})
	vcsClient.EXPECT().
		CommitStatus(mock.Anything, pr, pkg.StateRunning).
		RunAndReturn(func(ctx context.Context, _ vcs.PullRequest, state pkg.CommitState) error {
			close(inFlight)
			<-ctx.Done()
			record(state)
			return ctx.Err()
		}).
		Once()
	vcsClient.EXPECT().
		CommitStatus(mock.Anything, pr, pkg.StateError).
		RunAndReturn(func(_ context.Context, _ vcs.PullRequest, state pkg.CommitState) error {
			record(state)
			return nil
		}).
		Once()
	vcsClient.EXPECT().PostMessage(mock.Anything, pr, mock.Anything).Return(nil, nil).Once()

	request := newCheckRequest(EnqueueParams{PullRequest: pr, Container: ctr})
	go postQueuedStatus(request)
	<-inFlight

	rq := &RepoQueue{repoURL: pr.CloneURL, queue: make(chan *CheckRequest, 1)}
	rq.queue <- request

	start := time.Now()
	rq.notifyDroppedRequests(context.Background())

	assert.Less(t, time.Since(start), queuedStatusTimeout/2, "shutdown should cancel the running status, not wait it out")
	assert.Equal(t, []pkg.CommitState{pkg.StateRunning, pkg.StateError}, order)
}

func TestNotifyDroppedRequests_CommentsWhenStatusFails(t *testing.T) {
	vcsClient := vcsmocks.NewMockClient(t)
	ctr := container.Container{VcsClient: vcsClient}
	pr := testPullRequest(1, "aaa")

	vcsClient.EXPECT().CommitStatus(mock.Anything, pr, pkg.StateError).Return(errors.New("unavailable")).Once()
	vcsClient.EXPECT().PostMessage(mock.Anything, pr, mock.Anything).Return(nil, nil).Once()

	rq := &RepoQueue{repoURL: pr.CloneURL, queue: make(chan *CheckRequest, 1)}
	rq.queue <- queuedRequest(ctr, pr)

	rq.notifyDroppedRequests(context.Background())
}

func TestNotifyDroppedRequests_SetsAllStatusesInParallelBeforeComments(t *testing.T) {
	vcsClient := vcsmocks.NewMockClient(t)
	ctr := container.Container{VcsClient: vcsClient}
	const n = droppedRequestConcurrency
	const callTime = 200 * time.Millisecond

	var mu sync.Mutex
	var order []string
	vcsClient.EXPECT().
		CommitStatus(mock.Anything, mock.Anything, pkg.StateError).
		RunAndReturn(func(context.Context, vcs.PullRequest, pkg.CommitState) error {
			time.Sleep(callTime)
			mu.Lock()
			defer mu.Unlock()
			order = append(order, "status")
			return nil
		}).
		Times(n)
	vcsClient.EXPECT().
		PostMessage(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, vcs.PullRequest, string) (*msg.Message, error) {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, "comment")
			return nil, nil
		}).
		Times(n)

	rq := &RepoQueue{repoURL: "https://gitlab.com/example/repo.git", queue: make(chan *CheckRequest, n)}
	for i := 0; i < n; i++ {
		rq.queue <- queuedRequest(ctr, testPullRequest(i, fmt.Sprintf("sha%d", i)))
	}

	start := time.Now()
	rq.notifyDroppedRequests(context.Background())

	assert.Less(t, time.Since(start), n*callTime/2, "status updates should run in parallel")
	for i, step := range order {
		if i < n {
			assert.Equal(t, "status", step)
		} else {
			assert.Equal(t, "comment", step)
		}
	}
}

func TestNotifyDroppedRequests_StopsAtShutdownDeadline(t *testing.T) {
	vcsClient := vcsmocks.NewMockClient(t)
	ctr := container.Container{VcsClient: vcsClient}
	const n = 3 * droppedRequestConcurrency

	waitForCtx := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	vcsClient.EXPECT().
		CommitStatus(mock.Anything, mock.Anything, pkg.StateError).
		RunAndReturn(func(ctx context.Context, _ vcs.PullRequest, _ pkg.CommitState) error { return waitForCtx(ctx) }).
		Times(n)
	vcsClient.EXPECT().
		PostMessage(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ vcs.PullRequest, _ string) (*msg.Message, error) {
			return nil, waitForCtx(ctx)
		}).
		Times(n)

	rq := &RepoQueue{repoURL: "https://gitlab.com/example/repo.git", queue: make(chan *CheckRequest, n)}
	for i := 0; i < n; i++ {
		rq.queue <- queuedRequest(ctr, testPullRequest(i, fmt.Sprintf("sha%d", i)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	rq.notifyDroppedRequests(ctx)

	assert.Less(t, time.Since(start), droppedRequestTimeout, "cleanup must not outlive the shutdown context")
}
