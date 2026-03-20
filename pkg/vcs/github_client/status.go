package github_client

import (
	"context"

	"github.com/google/go-github/v74/github"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func toGithubCommitStatus(state pkg.CommitState) *string {
	switch state {
	case pkg.StateError, pkg.StatePanic:
		return pkg.Pointer("error")
	case pkg.StateFailure:
		return pkg.Pointer("failure")
	case pkg.StateRunning:
		return pkg.Pointer("pending")
	case pkg.StateSuccess, pkg.StateWarning, pkg.StateNone, pkg.StateSkip:
		return pkg.Pointer("success")
	}

	log.Warn().Str("state", state.BareString()).Msg("failed to convert to a github commit status")
	return pkg.Pointer("failure")
}

func (c *Client) CommitStatus(ctx context.Context, pr vcs.PullRequest, status pkg.CommitState) error {
	log.Info().Str("repo", pr.Name).Str("sha", pr.SHA).Str("status", status.BareString()).Msg("setting Github commit status")
	repoStatus, _, err := c.googleClient.Repositories.CreateStatus(ctx, pr.Owner, pr.Name, pr.SHA, &github.RepoStatus{
		State:       toGithubCommitStatus(status),
		Description: pkg.Pointer(status.BareString()),
		ID:          pkg.Pointer(int64(pr.CheckID)),
		Context:     pkg.Pointer("kubechecks"),
	})
	if err != nil {
		log.Err(err).Msg("could not set Github commit status")
		return err
	}
	log.Debug().Caller().Interface("status", repoStatus).Msg("Github commit status set")
	return nil
}
