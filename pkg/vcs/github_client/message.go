package github_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/go-github/v74/github"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/shurcooL/githubv4"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

const MaxCommentLength = 64 * 1024

func (c *Client) PostMessage(ctx context.Context, pr vcs.PullRequest, message string) (*msg.Message, error) {
	_, span := tracer.Start(ctx, "PostMessageToMergeRequest")
	defer span.End()

	log.Debug().Msgf("Posting message to PR %d in repo %s", pr.CheckID, pr.FullName)

	var comment *github.IssueComment
	err := backoff.Retry(func() error {
		cm, resp, err := c.googleClient.Issues.CreateComment(
			ctx,
			pr.Owner,
			pr.Name,
			pr.CheckID,
			&github.IssueComment{Body: &message},
		)
		comment = cm
		return checkReturnForBackoff(resp, err)
	}, getBackOff())

	if err != nil {
		telemetry.SetError(span, err, "Create Pull Request comment")
		return nil, errors.Wrap(err, "could not post message to PR")
	}

	return msg.NewMessage(pr.FullName, pr.CheckID, int(*comment.ID), c), nil
}

func (c *Client) UpdateMessage(ctx context.Context, pr vcs.PullRequest, m *msg.Message, messages []string) error {
	_, span := tracer.Start(ctx, "UpdateMessage")
	defer span.End()

	log.Info().Msgf("Updating message for PR %d in repo %s", m.CheckID, m.Name)

	for i, message := range messages {
		if i == 0 {
			var comment *github.IssueComment
			var resp *github.Response
			var err error

			repoNameComponents := strings.Split(m.Name, "/")
			err = backoff.Retry(func() error {
				comment, resp, err = c.googleClient.Issues.EditComment(
					ctx,
					repoNameComponents[0],
					repoNameComponents[1],
					int64(m.NoteID),
					&github.IssueComment{Body: &message},
				)
				return checkReturnForBackoff(resp, err)
			}, getBackOff())

			if err != nil {
				telemetry.SetError(span, err, "Update Pull Request comment")
				log.Error().Err(err).Msgf("could not update message to PR, response: %+v", resp)
				return err
			}

			// update note id just in case it changed
			m.NoteID = int(*comment.ID)
		} else {
			continuedHeader := fmt.Sprintf(
				"> Continued from previous [comment](%s)\n",
				fmt.Sprintf("%s/%s/%s/pull/%d#issuecomment-%d", c.cfg.VcsBaseUrl, pr.Owner, pr.Name, pr.CheckID, m.NoteID),
			)

			message = fmt.Sprintf("%s\n\n%s", continuedHeader, message)
			n, err := c.PostMessage(ctx, pr, message)
			if err != nil {
				log.Error().Err(err).Msg("could not post message to PR")
				return err
			}
			m.NoteID = n.NoteID
		}
	}

	return nil
}

// Pull all comments for the specified PR, and delete any comments that already exist from the bot
// This is different from updating an existing message, as this will delete comments from previous runs of the bot
// Whereas updates occur mid-execution
func (c *Client) pruneOldComments(
	ctx context.Context, pr vcs.PullRequest, comments []*github.IssueComment) error {
	_, span := tracer.Start(ctx, "pruneOldComments")
	defer span.End()

	log.Debug().Msgf("Pruning messages from PR %d in repo %s", pr.CheckID, pr.FullName)

	for _, comment := range comments {
		if strings.EqualFold(comment.GetUser().GetLogin(), c.username) || strings.Contains(*comment.Body, fmt.Sprintf("Kubechecks %s Report", c.cfg.Identifier)) {
			err := backoff.Retry(func() error {
				resp, err := c.googleClient.Issues.DeleteComment(ctx, pr.Owner, pr.Name, *comment.ID)
				return checkReturnForBackoff(resp, err)
			}, getBackOff())
			if err != nil {
				return fmt.Errorf("failed to delete comment: %w", err)
			}
		}
	}

	return nil
}

func (c *Client) hideOutdatedMessages(ctx context.Context, pr vcs.PullRequest, comments []*github.IssueComment) error {
	_, span := tracer.Start(ctx, "hideOutdatedComments")
	defer span.End()

	log.Debug().Msgf("Hiding kubecheck messages in PR %d in repo %s", pr.CheckID, pr.FullName)

	for _, comment := range comments {
		if strings.EqualFold(comment.GetUser().GetLogin(), c.username) || strings.Contains(*comment.Body, fmt.Sprintf("Kubechecks %s Report", c.cfg.Identifier)) {
			// Github API does not expose minimizeComment API. IT's only available from the GraphQL API
			// https://docs.github.com/en/graphql/reference/mutations#minimizecomment
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
			if err := c.shurcoolClient.Mutate(ctx, &m, input, nil); err != nil {
				return fmt.Errorf("minimize comment %s: %w", comment.GetNodeID(), err)
			}
		}
	}

	return nil

}

func (c *Client) TidyOutdatedComments(ctx context.Context, pr vcs.PullRequest) error {
	_, span := tracer.Start(ctx, "TidyOutdatedComments")
	defer span.End()

	var allComments []*github.IssueComment
	nextPage := 0

	for {
		log.Debug().Msgf("Listing comments for PR %d in repo %s", pr.CheckID, pr.FullName)
		var comments []*github.IssueComment
		var resp *github.Response
		var err error

		err = backoff.Retry(func() error {
			comments, resp, err = c.googleClient.Issues.ListComments(ctx, pr.Owner, pr.Name, pr.CheckID, &github.IssueListCommentsOptions{
				Sort:        pkg.Pointer("created"),
				Direction:   pkg.Pointer("asc"),
				ListOptions: github.ListOptions{Page: nextPage},
			})
			return checkReturnForBackoff(resp, err)
		}, getBackOff())

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

	if strings.ToLower(c.cfg.TidyOutdatedCommentsMode) == "delete" {
		return c.pruneOldComments(ctx, pr, allComments)
	}
	return c.hideOutdatedMessages(ctx, pr, allComments)
}

func (c *Client) GetMaxCommentLength() int {
	return MaxCommentLength
}

func (c *Client) GetPrCommentLinkTemplate(pr vcs.PullRequest) string {
	return fmt.Sprintf("%s/%s/%s/pull/%d#issuecomment-0000000000", c.cfg.VcsBaseUrl, pr.Owner, pr.Name, pr.CheckID)
}
