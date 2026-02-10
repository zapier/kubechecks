package gitea_client

import (
	"context"
	"fmt"
	"strings"

	"code.gitea.io/sdk/gitea"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

const MaxCommentLength = 256 * 1024

func (c *Client) PostMessage(ctx context.Context, pr vcs.PullRequest, message string) (*msg.Message, error) {
	_, span := tracer.Start(ctx, "PostMessage")
	defer span.End()

	if len(message) > MaxCommentLength {
		log.Warn().Int("original_length", len(message)).Msg("trimming the comment size")
		message = message[:MaxCommentLength]
	}

	log.Debug().Caller().Msgf("Posting message to PR %d in repo %s", pr.CheckID, pr.FullName)

	comment, _, err := c.g.Issues.CreateIssueComment(pr.Owner, pr.Name, int64(pr.CheckID), gitea.CreateIssueCommentOption{
		Body: message,
	})
	if err != nil {
		telemetry.SetError(span, err, "Create Issue Comment")
		return nil, errors.Wrap(err, "could not post message to PR")
	}

	return msg.NewMessage(pr.FullName, pr.CheckID, int(comment.ID), c), nil
}

func (c *Client) UpdateMessage(ctx context.Context, m *msg.Message, message string) error {
	_, span := tracer.Start(ctx, "UpdateMessage")
	defer span.End()

	if len(message) > MaxCommentLength {
		log.Warn().Int("original_length", len(message)).Msg("trimming the comment size")
		message = message[:MaxCommentLength]
	}

	log.Info().Msgf("Updating message for PR %d in repo %s", m.CheckID, m.Name)

	repoNameComponents := strings.Split(m.Name, "/")
	comment, _, err := c.g.Issues.EditIssueComment(
		repoNameComponents[0],
		repoNameComponents[1],
		int64(m.NoteID),
		gitea.EditIssueCommentOption{Body: message},
	)
	if err != nil {
		telemetry.SetError(span, err, "Update Issue Comment")
		log.Error().Err(err).Msgf("could not update message to PR, response: %+v", nil)
		return err
	}

	m.NoteID = int(comment.ID)
	return nil
}

func (c *Client) TidyOutdatedComments(ctx context.Context, pr vcs.PullRequest) error {
	_, span := tracer.Start(ctx, "TidyOutdatedComments")
	defer span.End()

	log.Debug().Caller().Msg("Tidying outdated comments")

	var allComments []*gitea.Comment
	page := 1

	for {
		comments, _, err := c.g.Issues.ListIssueComments(pr.Owner, pr.Name, int64(pr.CheckID), gitea.ListIssueCommentOptions{
			ListOptions: gitea.ListOptions{
				Page:     page,
				PageSize: 50,
			},
		})
		if err != nil {
			telemetry.SetError(span, err, "List Issue Comments")
			return fmt.Errorf("failed listing comments: %w", err)
		}
		allComments = append(allComments, comments...)
		if len(comments) < 50 {
			break
		}
		page++
	}

	if strings.ToLower(c.cfg.TidyOutdatedCommentsMode) == "delete" {
		return c.pruneOldComments(ctx, pr, allComments)
	}
	return c.hideOutdatedMessages(ctx, pr, allComments)
}

func (c *Client) pruneOldComments(ctx context.Context, pr vcs.PullRequest, comments []*gitea.Comment) error {
	_, span := tracer.Start(ctx, "pruneOldComments")
	defer span.End()

	log.Debug().Caller().Msgf("Pruning messages from PR %d in repo %s", pr.CheckID, pr.FullName)

	for _, comment := range comments {
		if c.isOurComment(comment) {
			_, err := c.g.Issues.DeleteIssueComment(pr.Owner, pr.Name, comment.ID)
			if err != nil {
				return fmt.Errorf("failed to delete comment: %w", err)
			}
		}
	}

	return nil
}

func (c *Client) hideOutdatedMessages(ctx context.Context, pr vcs.PullRequest, comments []*gitea.Comment) error {
	_, span := tracer.Start(ctx, "hideOutdatedComments")
	defer span.End()

	log.Debug().Caller().Msgf("Hiding kubechecks messages in PR %d in repo %s", pr.CheckID, pr.FullName)

	for _, comment := range comments {
		if !c.isOurComment(comment) {
			continue
		}

		// Skip already-hidden comments
		if strings.Contains(comment.Body, fmt.Sprintf("<summary><i>OUTDATED: Kubechecks %s Report</i></summary>", c.cfg.Identifier)) {
			continue
		}

		newBody := fmt.Sprintf(`
<details>
	<summary><i>OUTDATED: Kubechecks %s Report</i></summary>

%s
</details>
			`, c.cfg.Identifier, comment.Body)

		if len(newBody) > MaxCommentLength {
			log.Warn().Int("original_length", len(newBody)).Msg("trimming the comment size")
			newBody = newBody[:MaxCommentLength]
		}

		log.Debug().Caller().Msgf("Updating comment %d as outdated", comment.ID)

		_, _, err := c.g.Issues.EditIssueComment(pr.Owner, pr.Name, comment.ID, gitea.EditIssueCommentOption{
			Body: newBody,
		})
		if err != nil {
			telemetry.SetError(span, err, "Hide Existing Comment")
			return fmt.Errorf("could not hide comment %d: %w", comment.ID, err)
		}
	}

	return nil
}

func (c *Client) isOurComment(comment *gitea.Comment) bool {
	if comment.Poster != nil && strings.EqualFold(comment.Poster.UserName, c.username) {
		return true
	}
	if strings.Contains(comment.Body, fmt.Sprintf("Kubechecks %s Report", c.cfg.Identifier)) {
		return true
	}
	return false
}
