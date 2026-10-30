package gitlab_client

import (
	"context"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/vcs"
)

// fakePipelines returns each listed response in turn, repeating the last one.
type fakePipelines struct {
	responses [][]*gitlab.PipelineInfo
	calls     int
}

func (f *fakePipelines) ListProjectPipelines(interface{}, *gitlab.ListProjectPipelinesOptions, ...gitlab.RequestOptionFunc) ([]*gitlab.PipelineInfo, *gitlab.Response, error) {
	i := f.calls
	if i >= len(f.responses) {
		i = len(f.responses) - 1
	}
	f.calls++
	return f.responses[i], nil, nil
}

func (f *fakePipelines) reset(responses ...[]*gitlab.PipelineInfo) {
	f.responses = responses
	f.calls = 0
}

type fakeCommits struct {
	set []gitlab.SetCommitStatusOptions

	// implicitPipelineID is the pipeline GitLab reports when no PipelineID is passed.
	implicitPipelineID int64
}

func (f *fakeCommits) SetCommitStatus(_ interface{}, _ string, opt *gitlab.SetCommitStatusOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.CommitStatus, *gitlab.Response, error) {
	f.set = append(f.set, *opt)
	pipelineID := f.implicitPipelineID
	if opt.PipelineID != nil {
		pipelineID = *opt.PipelineID
	}
	return &gitlab.CommitStatus{PipelineID: pipelineID}, nil, nil
}

func (f *fakeCommits) pipelineIDs() []int64 {
	var ids []int64
	for _, s := range f.set {
		if s.PipelineID == nil {
			ids = append(ids, 0)
			continue
		}
		ids = append(ids, *s.PipelineID)
	}
	return ids
}

func newStatusTestClient(responses ...[]*gitlab.PipelineInfo) (*Client, *fakePipelines, *fakeCommits) {
	pipelines := &fakePipelines{responses: responses}
	commits := &fakeCommits{}
	c := &Client{
		c: &GLClient{Pipelines: pipelines, Commits: commits},
		newStatusBackOff: func() backoff.BackOff {
			return backoff.WithMaxRetries(&backoff.ZeroBackOff{}, 3)
		},
	}
	return c, pipelines, commits
}

var (
	testPR               = vcs.PullRequest{FullName: "example/repo", SHA: "abc"}
	externalPipeline     = &gitlab.PipelineInfo{ID: 1, Source: "external"}
	mergeRequestPipeline = &gitlab.PipelineInfo{ID: 2, Source: mergeRequestEventSource}
)

func TestCommitStatus_RunningWaitsForMergeRequestPipeline(t *testing.T) {
	c, _, commits := newStatusTestClient(
		[]*gitlab.PipelineInfo{externalPipeline},
		[]*gitlab.PipelineInfo{externalPipeline},
		[]*gitlab.PipelineInfo{externalPipeline, mergeRequestPipeline},
	)

	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateRunning))

	assert.Equal(t, []int64{mergeRequestPipeline.ID}, commits.pipelineIDs())
	assert.Equal(t, gitlab.Running, commits.set[0].State)
}

func TestCommitStatus_RunningFallsBackToBestPipeline(t *testing.T) {
	c, _, commits := newStatusTestClient([]*gitlab.PipelineInfo{externalPipeline})

	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateRunning))

	assert.Equal(t, []int64{externalPipeline.ID}, commits.pipelineIDs())
}

func TestCommitStatus_RunningWaitsForFirstPipeline(t *testing.T) {
	c, _, commits := newStatusTestClient(
		[]*gitlab.PipelineInfo{},
		[]*gitlab.PipelineInfo{},
		[]*gitlab.PipelineInfo{mergeRequestPipeline},
	)

	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateRunning))

	assert.Equal(t, []int64{mergeRequestPipeline.ID}, commits.pipelineIDs())
}

func TestCommitStatus_RunningWithoutPipelinesGivesUpAfterRetries(t *testing.T) {
	c, pipelines, commits := newStatusTestClient([]*gitlab.PipelineInfo{})

	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateRunning))

	assert.Equal(t, 6, pipelines.calls, "one lookup, one check for any pipelines, then 4 bounded attempts")
	assert.Equal(t, []int64{0}, commits.pipelineIDs())
}

func TestCommitStatus_FinalStateSkipsRetriesWithoutPipelines(t *testing.T) {
	c, pipelines, commits := newStatusTestClient([]*gitlab.PipelineInfo{})

	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateSuccess))

	assert.Equal(t, 2, pipelines.calls, "one lookup plus one check for any pipelines, no retries")
	assert.Equal(t, []int64{0}, commits.pipelineIDs())
}

func TestCommitStatus_FinalStateDoesNotWait(t *testing.T) {
	c, pipelines, commits := newStatusTestClient([]*gitlab.PipelineInfo{externalPipeline})

	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateFailure))

	assert.Equal(t, 1, pipelines.calls)
	assert.Equal(t, gitlab.Failed, commits.set[0].State)
}

func TestCommitStatus_FinalStatusFinishesEarlierRunningPipeline(t *testing.T) {
	c, pipelines, commits := newStatusTestClient([]*gitlab.PipelineInfo{externalPipeline})
	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateRunning))

	// The MR pipeline appears after the running status gave up waiting for it.
	pipelines.reset([]*gitlab.PipelineInfo{externalPipeline, mergeRequestPipeline})
	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateFailure))

	assert.Equal(t, []int64{externalPipeline.ID, externalPipeline.ID, mergeRequestPipeline.ID}, commits.pipelineIDs())
	assert.Equal(t, gitlab.Failed, commits.set[1].State)
	assert.Equal(t, gitlab.Failed, commits.set[2].State)
}

func TestCommitStatus_FinalStatusFinishesPipelineGitLabPickedForRunning(t *testing.T) {
	c, pipelines, commits := newStatusTestClient([]*gitlab.PipelineInfo{})
	commits.implicitPipelineID = 7
	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateRunning))

	pipelines.reset([]*gitlab.PipelineInfo{mergeRequestPipeline})
	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateFailure))

	assert.Equal(t, []int64{0, 7, mergeRequestPipeline.ID}, commits.pipelineIDs())
	assert.Equal(t, gitlab.Failed, commits.set[1].State)
}

func TestCommitStatus_FinalStatusOnSamePipelineIsSetOnce(t *testing.T) {
	c, _, commits := newStatusTestClient([]*gitlab.PipelineInfo{mergeRequestPipeline})
	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateRunning))
	require.NoError(t, c.CommitStatus(context.Background(), testPR, pkg.StateSuccess))

	assert.Equal(t, []int64{mergeRequestPipeline.ID, mergeRequestPipeline.ID}, commits.pipelineIDs())
}

func TestCommitStatus_CancelledContextSetsNothing(t *testing.T) {
	c, _, commits := newStatusTestClient([]*gitlab.PipelineInfo{externalPipeline})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.ErrorIs(t, c.CommitStatus(ctx, testPR, pkg.StateRunning), context.Canceled)
	assert.Empty(t, commits.set)
}
