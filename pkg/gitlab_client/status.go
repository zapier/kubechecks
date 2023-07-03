package gitlab_client

import (
	"context"

	"github.com/xanzy/go-gitlab"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
)

func (c *Client) CommitStatus(ctx context.Context, repo *repo.Repo, state vcs_clients.CommitState) error {
	var pipelineID = c.GetLastPipelinesForCommit(repo.Name, repo.SHA).ID
	status := &gitlab.SetCommitStatusOptions{
		Name:        gitlab.String("kubechecks"),
		Context:     gitlab.String("kubechecks"),
		Description: gitlab.String(state.StateToDesc()),
		State:       convertState(state),
		PipelineID:  &pipelineID,
	}
	_, err := c.setCommitStatus(repo.Name, repo.SHA, status)
	if err != nil {
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
