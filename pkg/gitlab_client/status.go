package gitlab_client

import (
	"context"

	"github.com/xanzy/go-gitlab"
	"github.com/zapier/kubechecks/pkg/repo"
)

func (c *Client) CommitStatus(ctx context.Context, repo *repo.Repo, state string) error {
	desc, gitlabState := stateToDescAndValue(state)
	var pipelineID = 0
	status := &gitlab.SetCommitStatusOptions{
		Name:        gitlab.String("kubechecks"),
		Context:     gitlab.String("kubechecks"),
		Description: desc,
		State:       gitlabState,
		PipelineID:  &pipelineID, // TODO: Get pipeline ID
	}
	_, err := c.setCommitStatus(repo.Name, repo.SHA, status)
	if err != nil {
		return err
	}
	return nil
}

func stateToDescAndValue(state string) (*string, gitlab.BuildStateValue) {
	switch state {
	case "pending":
		return gitlab.String("pending..."), gitlab.Pending
	case "running":
		return gitlab.String("in progress..."), gitlab.Running
	case "failure":
		return gitlab.String("failed."), gitlab.Failed
	case "success":
		return gitlab.String("succeeded."), gitlab.Success
	}
	return gitlab.String("unknown"), gitlab.Failed
}

func (c *Client) setCommitStatus(projectWithNS string, commitSHA string, status *gitlab.SetCommitStatusOptions) (*gitlab.CommitStatus, error) {
	commitStatus, _, err := c.Commits.SetCommitStatus(projectWithNS, commitSHA, status)
	return commitStatus, err
}
