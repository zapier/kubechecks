package github_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/cenkalti/backoff/v5"
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

	comment, err := backoff.Retry(context.TODO(), func() (*github.IssueComment, error) {
		cm, _, err := c.googleClient.Issues.CreateComment(
			ctx,
			pr.Owner,
			pr.Name,
			pr.CheckID,
			&github.IssueComment{Body: &message},
		)
		return cm, err
	}, backoff.WithBackOff(backoff.NewExponentialBackOff()))

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

	for i, msg := range messages {
		if i == 0 {
			var comment *github.IssueComment
			var resp *github.Response
			var err error

			repoNameComponents := strings.Split(m.Name, "/")
			resp, err = backoff.Retry(context.TODO(), func() (*github.Response, error) {
				comment, resp, err = c.googleClient.Issues.EditComment(
					ctx,
					repoNameComponents[0],
					repoNameComponents[1],
					int64(m.NoteID),
					&github.IssueComment{Body: &msg},
				)
				return resp, err
			}, backoff.WithBackOff(backoff.NewExponentialBackOff()))

			if err != nil {
				telemetry.SetError(span, err, "Update Pull Request comment")
				log.Error().Err(err).Msgf("could not update message to PR, msg: %s, response: %+v", msg, resp)
				return err
			}

			// update note id just in case it changed
			m.NoteID = int(*comment.ID)
		} else {
			continuedHeader := fmt.Sprintf(
				"> Continued from previous [comment](%s)\n",
				fmt.Sprintf("https://github.com/%s/%s/pull/%d#issuecomment-%d", pr.Owner, pr.Name, pr.CheckID, m.NoteID),
			)

			msg = fmt.Sprintf("%s\n\n%s", continuedHeader, msg)
			n, err := c.PostMessage(ctx, pr, msg)
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
			_, err := c.googleClient.Issues.DeleteComment(ctx, pr.Owner, pr.Name, *comment.ID)
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
		comments, resp, err := c.googleClient.Issues.ListComments(ctx, pr.Owner, pr.Name, pr.CheckID, &github.IssueListCommentsOptions{
			Sort:        pkg.Pointer("created"),
			Direction:   pkg.Pointer("asc"),
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

	if strings.ToLower(c.cfg.TidyOutdatedCommentsMode) == "delete" {
		return c.pruneOldComments(ctx, pr, allComments)
	}
	return c.hideOutdatedMessages(ctx, pr, allComments)
}

func (c *Client) GetMaxCommentLength() int {
	return MaxCommentLength
}

func (c *Client) GetPrCommentLinkTemplate(pr vcs.PullRequest) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d#issuecomment-0000000000", pr.Owner, pr.Name, pr.CheckID)
}
