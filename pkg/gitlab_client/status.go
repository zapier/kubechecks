package gitlab_client

import "github.com/xanzy/go-gitlab"

func (c *Client) CommitStatus(state gitlab.BuildStateValue, actionName string, pipelineID *int, projectWithNS string, commitSHA string) error {
	status := &gitlab.SetCommitStatusOptions{
		Name:        gitlab.String(actionName),
		Context:     gitlab.String(actionName),
		Description: descriptionForState(state),
		State:       state,
		PipelineID:  pipelineID,
	}

	_, err := c.setCommitStatus(
		projectWithNS,
		commitSHA,
		status,
	)
	if err != nil {
		return err
	}

	return nil
}

func descriptionForState(state gitlab.BuildStateValue) *string {
	switch state {
	case gitlab.Pending:
		return gitlab.String("pending...")
	case gitlab.Running:
		return gitlab.String("in progress...")
	case gitlab.Failed:
		return gitlab.String("failed.")
	case gitlab.Success:
		return gitlab.String("succeeded.")
	}
	return gitlab.String("unknown")
}

func (c *Client) setCommitStatus(projectWithNS string, commitSHA string, status *gitlab.SetCommitStatusOptions) (*gitlab.CommitStatus, error) {
	commitStatus, _, err := c.Commits.SetCommitStatus(projectWithNS, commitSHA, status)
	return commitStatus, err
}
