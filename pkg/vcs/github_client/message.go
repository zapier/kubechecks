package github_client

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// a long report is many calls, each with its own retries
const updateMessageTimeout = 10 * time.Minute

func (c *Client) MaxCommentLength() int { return MaxCommentLength }

func (c *Client) PostMessage(ctx context.Context, pr vcs.PullRequest, message string) (*msg.Message, error) {
	_, span := tracer.Start(ctx, "PostMessageToMergeRequest")
	defer span.End()

	if len(message) > MaxCommentLength {
		log.Warn().Int("original_length", len(message)).Msg("trimming the comment size")
		message = message[:MaxCommentLength]
	}

	log.Debug().Caller().Msgf("Posting message to PR %d in repo %s", pr.CheckID, pr.FullName)
	comment, _, err := c.googleClient.Issues.CreateComment(
		ctx,
		pr.Owner,
		pr.Name,
		pr.CheckID,
		&github.IssueComment{Body: &message},
	)

	if err != nil {
		telemetry.SetError(span, err, "Create Pull Request comment")
		return nil, errors.Wrap(err, "could not post message to PR")
	}

	return msg.NewMessage(pr.FullName, pr.CheckID, int(*comment.ID), c), nil
}

func (c *Client) UpdateMessage(ctx context.Context, pr vcs.PullRequest, noteID int, chunks []string) error {
	_, span := tracer.Start(ctx, "UpdateMessage")
	defer span.End()

	ctx, cancel := context.WithTimeout(ctx, updateMessageTimeout)
	defer cancel()

	log.Info().Int("chunks", len(chunks)).Msgf("Updating message for PR %d in repo %s", pr.CheckID, pr.FullName)
	rc := c.commentRetry.withDefaults(3, 2*time.Second, 30*time.Second)

	for i, chunk := range chunks {
		if len(chunk) > MaxCommentLength {
			log.Warn().Int("original_length", len(chunk)).Msg("trimming the comment size")
			chunk = chunk[:MaxCommentLength]
		}

		// a create that failed late may still have landed, doing it again would post the chunk twice
		edit := i == 0

		err := rc.do(ctx, "posting comment", edit, func() (*github.Response, error) {
			if edit {
				_, resp, err := c.googleClient.Issues.EditComment(ctx, pr.Owner, pr.Name, int64(noteID), &github.IssueComment{Body: &chunk})
				return resp, err
			}
			_, resp, err := c.googleClient.Issues.CreateComment(ctx, pr.Owner, pr.Name, pr.CheckID, &github.IssueComment{Body: &chunk})
			return resp, err
		})
		if err != nil {
			telemetry.SetError(span, err, "Update Pull Request comment")
			log.Error().Err(err).Int("chunk", i+1).Msg("could not update message to PR")
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("gave up after %s, posting comment %d of %d: %w", updateMessageTimeout, i+1, len(chunks), err)
			}
			return fmt.Errorf("posting comment %d of %d: %w", i+1, len(chunks), err)
		}
	}

	return nil
}

// Pull all comments for the specified PR, and delete any comments that already exist from the bot
// This is different from updating an existing message, as this will delete comments from previous runs of the bot
// Whereas updates occur mid-execution
func (c *Client) pruneOldComments(ctx context.Context, pr vcs.PullRequest, comments []*github.IssueComment) error {
	_, span := tracer.Start(ctx, "pruneOldComments")
	defer span.End()

	log.Debug().Caller().Msgf("Pruning messages from PR %d in repo %s", pr.CheckID, pr.FullName)

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

	log.Debug().Caller().Msgf("Hiding kubecheck messages in PR %d in repo %s", pr.CheckID, pr.FullName)

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
