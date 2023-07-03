package github_client

import (
	"context"
	"strings"
	"sync"

	"github.com/google/go-github/github"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
	"github.com/zapier/kubechecks/telemetry"
	"go.opentelemetry.io/otel"
)

func (c *Client) PostMessage(ctx context.Context, projectName string, prID int, msg string) *vcs_clients.Message {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "PostMessageToMergeRequest")
	defer span.End()

	// As this is our first time posting a comment for this run, delete any existing comments
	err := c.pruneOldComments(ctx, projectName, prID)
	if err != nil {
		telemetry.SetError(span, err, "Prune old comments")
		log.Error().Err(err).Msg("could not prune old comments")
		// Continue anyway if we can't delete old ones
	}

	repoNameComponents := strings.Split(projectName, "/")
	log.Debug().Msgf("Posting message to PR %d in repo %s", prID, projectName)
	comment, _, err := c.Issues.CreateComment(
		ctx,
		repoNameComponents[0],
		repoNameComponents[1],
		prID,
		&github.IssueComment{Body: &msg},
	)

	if err != nil {
		telemetry.SetError(span, err, "Create Pull Request comment")
		log.Error().Err(err).Msg("could not post message to PR")
	}

	return &vcs_clients.Message{
		Lock:    sync.Mutex{},
		Name:    projectName,
		CheckID: prID,
		NoteID:  int(*comment.ID),
		Msg:     msg,
		Client:  c,
		Apps:    make(map[string]string),
	}
}

func (c *Client) UpdateMessage(ctx context.Context, m *vcs_clients.Message, msg string) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "UpdateMessage")
	defer span.End()

	log.Info().Msgf("Updating message for PR %d in repo %s", m.CheckID, m.Name)

	repoNameComponents := strings.Split(m.Name, "/")
	comment, resp, err := c.Issues.EditComment(
		ctx,
		repoNameComponents[0],
		repoNameComponents[1],
		int64(m.NoteID),
		&github.IssueComment{Body: &msg},
	)

	if err != nil {
		telemetry.SetError(span, err, "Update Pull Request comment")
		log.Error().Err(err).Msgf("could not update message to PR, msg: %s, response: %+v", msg, resp)
		return err
	}

	// update note id just in case it changed
	m.NoteID = int(*comment.ID)

	return nil
}

// Pull all comments for the specified PR, and delete any comments that already exist from the bot
// This is different from updating an existing message, as this will delete comments from previous runs of the bot
// Whereas updates occur mid-execution
func (c *Client) pruneOldComments(ctx context.Context, projectName string, prID int) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "pruneOldComments")
	defer span.End()

	repoNameComponents := strings.Split(projectName, "/")
	log.Debug().Msgf("Pruning messages from PR %d in repo %s", prID, projectName)
	issueComments, _, err := c.Issues.ListComments(ctx, repoNameComponents[0], repoNameComponents[1], prID, nil)

	if err != nil {
		telemetry.SetError(span, err, "Get Issue Comments failed")
		log.Error().Err(err).Msg("could not get issue")
		return err
	}

	for _, comment := range issueComments {
		if comment.GetUser().GetLogin() == githubTokenUser {
			c.Issues.DeleteComment(ctx, repoNameComponents[0], repoNameComponents[1], *comment.ID)
		}
	}

	return nil

}
