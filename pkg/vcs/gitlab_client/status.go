package gitlab_client

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog/log"
	"gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/vcs"
)

const GitlabCommitStatusContext = "kubechecks"

const mergeRequestEventSource = "merge_request_event"

// runningStatusMaxWait caps how long a running status waits for the MR pipeline.
const runningStatusMaxWait = 10 * time.Second

var errNoPipelineStatus = errors.New("nil pipeline status")

func (c *Client) CommitStatus(ctx context.Context, pr vcs.PullRequest, state pkg.CommitState) error {
	description := fmt.Sprintf("%s %s", state.BareString(), c.ToEmoji(state))

	status := &gitlab.SetCommitStatusOptions{
		Name:        pkg.Pointer(GitlabCommitStatusContext),
		Context:     pkg.Pointer(GitlabCommitStatusContext),
		Description: pkg.Pointer(description),
		State:       convertState(state),
	}
	// Get pipelineStatus so we can attach new status to existing pipeline. We
	// retry a few times to avoid creating a duplicate external pipeline status if
	// another service is also setting it.
	var pipelineStatus *gitlab.PipelineInfo

	// Quick check: try to find pipeline once without retries
	log.Debug().Caller().Msg("checking for existing pipeline")
	pipelineStatus = c.GetLastPipelinesForCommit(ctx, pr.FullName, pr.SHA)

	// Only retry if we found pipelines but couldn't match one
	// This avoids wasteful retries for projects without CI/CD
	if !pipelineMatches(pipelineStatus, state) {
		log.Debug().Caller().Msg("no pipeline found on first attempt, checking if retries are needed")

		// Do a quick check for ANY pipelines on this commit (not just matching sources)
		// If there are pipelines but we didn't match them, retry to see if the right one appears
		pipelines, _, err := c.c.Pipelines.ListProjectPipelines(pr.FullName, &gitlab.ListProjectPipelinesOptions{
			SHA: pkg.Pointer(pr.SHA),
		}, gitlab.WithContext(ctx))

		if err == nil && len(pipelines) > 0 {
			// There are pipelines but we didn't match any - retry with backoff
			log.Debug().Caller().Int("pipeline_count", len(pipelines)).Msg("pipelines exist but none matched, retrying...")

			getStatusFn := func() error {
				p := c.GetLastPipelinesForCommit(ctx, pr.FullName, pr.SHA)
				if p != nil {
					pipelineStatus = p
				}
				if !pipelineMatches(p, state) {
					return errNoPipelineStatus
				}
				return nil
			}
			retryErr := backoff.Retry(getStatusFn, c.statusBackOff(ctx, state))
			if retryErr != nil {
				log.Debug().Msg("could not find matching pipeline after retries")
			}
		} else {
			// No pipelines at all - likely no CI/CD configured, skip retries
			log.Debug().Caller().Msg("no pipelines found for commit, skipping retries (likely no CI/CD)")
		}
	}

	if pipelineStatus != nil {
		log.Trace().Int64("pipeline_id", pipelineStatus.ID).Msg("pipeline status")
		status.PipelineID = &pipelineStatus.ID
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	runningKey := pr.FullName + "@" + pr.SHA
	if state != pkg.StateRunning {
		c.finishRunningPipeline(ctx, runningKey, pr, status)
	}

	log.Debug().
		Caller().
		Str("project", pr.FullName).
		Str("commit_sha", pr.SHA).
		Str("kubechecks_status", description).
		Str("gitlab_status", string(status.State)).
		Msg("gitlab client: updating commit status")
	_, setErr := c.setCommitStatus(ctx, pr.FullName, pr.SHA, status)
	if setErr != nil {
		log.Error().Err(setErr).Str("project", pr.FullName).Msg("gitlab client: could not set commit status")
		return setErr
	}
	if state == pkg.StateRunning && status.PipelineID != nil {
		c.runningPipelines.Store(runningKey, *status.PipelineID)
	}
	return nil
}

// pipelineMatches reports whether p is the pipeline a status for state should
// attach to. A running status is often posted as soon as the MR opens, before
// GitLab creates the MR pipeline, so it waits briefly for that pipeline: on
// projects with MR pipelines it is the MR's head pipeline, the one merge when
// pipeline succeeds evaluates. Projects without MR pipelines fall back to the
// best pipeline found once the wait expires.
func pipelineMatches(p *gitlab.PipelineInfo, state pkg.CommitState) bool {
	if p == nil {
		return false
	}
	return state != pkg.StateRunning || p.Source == mergeRequestEventSource
}

// finishRunningPipeline sets the final state on the pipeline an earlier running
// status was attached to, when the final status goes to a different pipeline.
// Otherwise that job would stay running in the GitLab UI.
func (c *Client) finishRunningPipeline(ctx context.Context, key string, pr vcs.PullRequest, final *gitlab.SetCommitStatusOptions) {
	v, ok := c.runningPipelines.LoadAndDelete(key)
	if !ok {
		return
	}
	id := v.(int64)
	if final.PipelineID != nil && *final.PipelineID == id {
		return
	}

	previous := *final
	previous.PipelineID = &id
	if _, err := c.setCommitStatus(ctx, pr.FullName, pr.SHA, &previous); err != nil {
		log.Warn().Err(err).Int64("pipeline_id", id).Str("project", pr.FullName).Msg("gitlab client: could not finish earlier running status")
	}
}

func convertState(state pkg.CommitState) gitlab.BuildStateValue {
	switch state {
	case pkg.StateRunning:
		return gitlab.Running
	case pkg.StateFailure, pkg.StateError, pkg.StatePanic:
		return gitlab.Failed
	case pkg.StateSuccess, pkg.StateWarning, pkg.StateNone, pkg.StateSkip:
		return gitlab.Success
	}

	log.Warn().Str("state", strconv.FormatUint(uint64(state), 10)).Msg("cannot convert to gitlab state")
	return gitlab.Failed
}

func (c *Client) setCommitStatus(ctx context.Context, projectWithNS string, commitSHA string, status *gitlab.SetCommitStatusOptions) (*gitlab.CommitStatus, error) {
	commitStatus, _, err := c.c.Commits.SetCommitStatus(projectWithNS, commitSHA, status, gitlab.WithContext(ctx))
	return commitStatus, err
}

// statusBackOff returns the retry policy for finding a pipeline to attach a
// status to. It stops when ctx is cancelled, and a running status waits at most
// runningStatusMaxWait.
func (c *Client) statusBackOff(ctx context.Context, state pkg.CommitState) backoff.BackOff {
	if c.newStatusBackOff != nil {
		return backoff.WithContext(c.newStatusBackOff(), ctx)
	}
	b := configureBackOff()
	if state == pkg.StateRunning {
		b.MaxElapsedTime = runningStatusMaxWait
	}
	return backoff.WithContext(b, ctx)
}

// configureBackOff returns a backoff configuration to use to retry requests
func configureBackOff() *backoff.ExponentialBackOff {

	// Lets setup backoff logic to retry this request for 30 seconds
	expBackOff := backoff.NewExponentialBackOff()
	expBackOff.MaxInterval = 10 * time.Second
	expBackOff.MaxElapsedTime = 30 * time.Second

	return expBackOff
}

type CommitsServices interface {
	SetCommitStatus(pid interface{}, sha string, opt *gitlab.SetCommitStatusOptions, options ...gitlab.RequestOptionFunc) (*gitlab.CommitStatus, *gitlab.Response, error)
}

type CommitsService struct {
	CommitsServices
}
