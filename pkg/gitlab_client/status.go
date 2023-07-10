package gitlab_client

import (
	"context"
	"errors"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog/log"
	"github.com/xanzy/go-gitlab"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
)

const GitlabCommitStatusContext = "kubechecks"

var nilPipelineStatus = errors.New("nil pipeline status")

func (c *Client) CommitStatus(ctx context.Context, repo *repo.Repo, state vcs_clients.CommitState) error {
	status := &gitlab.SetCommitStatusOptions{
		Name:        gitlab.String(GitlabCommitStatusContext),
		Context:     gitlab.String(GitlabCommitStatusContext),
		Description: gitlab.String(state.StateToDesc()),
		State:       convertState(state),
	}
	// Get pipelineStatus so we can attach new status to existing pipeline. We
	// retry a few times to avoid creating a duplicate external pipeline status if
	// another service is also setting it.
	var pipelineStatus *gitlab.PipelineInfo
	getStatusFn := func() error {
		log.Debug().Msg("getting pipeline status")
		pipelineStatus = c.GetLastPipelinesForCommit(repo.OwnerName, repo.SHA)
		if pipelineStatus == nil {
			return nilPipelineStatus
		}
		return nil
	}
	err := backoff.Retry(getStatusFn, configureBackOff())
	if err != nil {
		log.Warn().Msg("could not retrieve pipeline status after multiple attempts")
	}
	if pipelineStatus != nil {
		log.Trace().Int("pipeline_id", pipelineStatus.ID).Msg("pipeline status")
		status.PipelineID = &pipelineStatus.ID
	}

	log.Debug().Str("project", repo.OwnerName).Str("commit_sha", repo.SHA).Msg("gitlab client: updating commit status")
	_, err = c.setCommitStatus(repo.OwnerName, repo.SHA, status)
	if err != nil {
		log.Error().Err(err).Str("project", repo.OwnerName).Msg("gitlab client: could not set commit status")
		return err
	}
	return nil
}

func convertState(state vcs_clients.CommitState) gitlab.BuildStateValue {
	switch state {
	case vcs_clients.Pending:
		return gitlab.Pending
	case vcs_clients.Running:
		return gitlab.Running
	case vcs_clients.Failure:
		return gitlab.Failed
	case vcs_clients.Success:
		return gitlab.Success
	}
	return gitlab.Failed
}

func (c *Client) setCommitStatus(projectWithNS string, commitSHA string, status *gitlab.SetCommitStatusOptions) (*gitlab.CommitStatus, error) {
	commitStatus, _, err := c.Commits.SetCommitStatus(projectWithNS, commitSHA, status)
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
