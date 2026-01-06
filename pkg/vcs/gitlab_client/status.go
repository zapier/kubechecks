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
	if pipelineStatus == nil {
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
				pipelineStatus = c.GetLastPipelinesForCommit(ctx, pr.FullName, pr.SHA)
				if pipelineStatus == nil {
					return errNoPipelineStatus
				}
				return nil
			}
			retryErr := backoff.Retry(getStatusFn, configureBackOff())
			if retryErr != nil {
				log.Debug().Msg("could not find matching pipeline after retries")
			}
		} else {
			// No pipelines at all - likely no CI/CD configured, skip retries
			log.Debug().Caller().Msg("no pipelines found for commit, skipping retries (likely no CI/CD)")
		}
	}

	if pipelineStatus != nil {
		log.Trace().Int("pipeline_id", pipelineStatus.ID).Msg("pipeline status")
		status.PipelineID = &pipelineStatus.ID
	}

	log.Debug().
		Caller().
		Str("project", pr.FullName).
		Str("commit_sha", pr.SHA).
		Str("kubechecks_status", description).
		Str("gitlab_status", string(status.State)).
		Msg("gitlab client: updating commit status")
	_, setErr := c.setCommitStatus(pr.FullName, pr.SHA, status)
	if setErr != nil {
		log.Error().Err(setErr).Str("project", pr.FullName).Msg("gitlab client: could not set commit status")
		return setErr
	}
	return nil
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

func (c *Client) setCommitStatus(projectWithNS string, commitSHA string, status *gitlab.SetCommitStatusOptions) (*gitlab.CommitStatus, error) {
	commitStatus, _, err := c.c.Commits.SetCommitStatus(projectWithNS, commitSHA, status)
	return commitStatus, err
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
