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
	}
}

func (c *Client) UpdateMessage(ctx context.Context, m *vcs_clients.Message, msg string) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "UpdateMessage")
	defer span.End()

	repoNameComponents := strings.Split(m.Name, "/")
	comment, _, err := c.Issues.EditComment(
		ctx,
		repoNameComponents[0],
		repoNameComponents[1],
		int64(m.NoteID),
		&github.IssueComment{Body: &msg},
	)

	if err != nil {
		telemetry.SetError(span, err, "Update Pull Request comment")
		log.Error().Err(err).Msg("could not update message to PR")
		return err
	}

	// update note id just in case it changed
	m.NoteID = int(*comment.ID)

	return nil
}
