package github_client

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/google/go-github/v53/github"
	"github.com/rs/zerolog/log"
	"github.com/shurcooL/githubv4"
	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
	"github.com/zapier/kubechecks/telemetry"
	"go.opentelemetry.io/otel"
)

func (c *Client) PostMessage(ctx context.Context, repo *repo.Repo, prID int, msg string) *vcs_clients.Message {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "PostMessageToMergeRequest")
	defer span.End()

	log.Debug().Msgf("Posting message to PR %d in repo %s", prID, repo.FullName)
	comment, _, err := c.Issues.CreateComment(
		ctx,
		repo.Owner,
		repo.Name,
		prID,
		&github.IssueComment{Body: &msg},
	)

	if err != nil {
		telemetry.SetError(span, err, "Create Pull Request comment")
		log.Error().Err(err).Msg("could not post message to PR")
	}

	return &vcs_clients.Message{
		Lock:    sync.Mutex{},
		Name:    repo.FullName,
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
func (c *Client) pruneOldComments(ctx context.Context, repo *repo.Repo, comments []*github.IssueComment) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "pruneOldComments")
	defer span.End()

	log.Debug().Msgf("Pruning messages from PR %d in repo %s", repo.CheckID, repo.FullName)

	for _, comment := range comments {
		if strings.EqualFold(comment.GetUser().GetLogin(), githubTokenUser) {
			_, err := c.Issues.DeleteComment(ctx, repo.Owner, repo.Name, *comment.ID)
			if err != nil {
				return fmt.Errorf("failed to delete comment: %w", err)
			}
		}
	}

	return nil
}

func (c *Client) hideOutdatedMessages(ctx context.Context, repo *repo.Repo, comments []*github.IssueComment) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "hideOutdatedComments")
	defer span.End()

	log.Debug().Msgf("Hiding kubecheck messages in PR %d in repo %s", repo.CheckID, repo.FullName)

	for _, comment := range comments {
		if strings.EqualFold(comment.GetUser().GetLogin(), githubTokenUser) {
			var m struct {
				MinimizeComment struct {
					MinimizedComment struct {
						IsMinimized       githubv4.Boolean
						MinimizedReason   githubv4.String
						ViewerCanMinimize githubv4.Boolean
					}
				} `graphql:"minimizeComment(input:$input)"`
			}
			input := githubv4.MinimizeCommentInput{
				Classifier: githubv4.ReportedContentClassifiersOutdated,
				SubjectID:  comment.GetNodeID(),
			}
			if err := c.v4Client.Mutate(ctx, &m, input, nil); err != nil {
				return fmt.Errorf("minimize comment %s: %w", comment.GetNodeID(), err)
			}
		}
	}

	return nil

}

func (c *Client) TidyOutdatedComments(ctx context.Context, repo *repo.Repo) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "TidyOutdatedComments")
	defer span.End()

	var allComments []*github.IssueComment
	nextPage := 0

	for {
		comments, resp, err := c.Issues.ListComments(ctx, repo.Owner, repo.Name, repo.CheckID, &github.IssueListCommentsOptions{
			Sort:        github.String("created"),
			Direction:   github.String("asc"),
			ListOptions: github.ListOptions{Page: nextPage},
		})
		if err != nil {
			telemetry.SetError(span, err, "Get Issue Comments failed")
			return fmt.Errorf("failed listing comments: %w", err)
		}
		allComments = append(allComments, comments...)
		if resp.NextPage == 0 {
			break
		}
		nextPage = resp.NextPage
	}

	if strings.ToLower(viper.GetString("tidy-outdated-comments-mode")) == "delete" {
		return c.pruneOldComments(ctx, repo, allComments)
	}
	return c.hideOutdatedMessages(ctx, repo, allComments)
}
