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

const maxCommentLength = 64 * 1024

func (c *Client) MaxCommentLength() int { return maxCommentLength }

func (c *Client) PostMessage(ctx context.Context, pr vcs.PullRequest, message string) (*msg.Message, error) {
	_, span := tracer.Start(ctx, "PostMessageToMergeRequest")
	defer span.End()

	if len(message) > maxCommentLength {
		telemetry.SetError(span, fmt.Errorf("message length %d exceeds limit %d", len(message), maxCommentLength), "PostMessage")
		return nil, fmt.Errorf("message length %d exceeds GitHub comment limit %d", len(message), maxCommentLength)
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

func (c *Client) UpdateMessage(ctx context.Context, pr vcs.PullRequest, m *msg.Message, chunks []string) error {
	_, span := tracer.Start(ctx, "UpdateMessage")
	defer span.End()

	log.Debug().Msgf("Deleting placeholder comment %d for PR %d in repo %s", m.NoteID, pr.CheckID, pr.FullName)
	if _, err := c.googleClient.Issues.DeleteComment(ctx, pr.Owner, pr.Name, int64(m.NoteID)); err != nil {
		telemetry.SetError(span, err, "Delete placeholder comment")
		log.Error().Err(err).Msg("failed to delete placeholder comment")
		return fmt.Errorf("deleting placeholder comment: %w", err)
	}

	log.Info().Int("chunks", len(chunks)).Msgf("Posting %d comment(s) to PR %d in repo %s", len(chunks), pr.CheckID, pr.FullName)
	rc := retryConfig{}.withDefaults(3, 2*time.Second, 30*time.Second)

	for i, chunk := range chunks {
		var cc *github.IssueComment
		backoff := rc.initialBackoff

		var lastErr error
		for attempt := range rc.maxRetries + 1 {
			cc, _, lastErr = c.googleClient.Issues.CreateComment(
				ctx, pr.Owner, pr.Name, pr.CheckID,
				&github.IssueComment{Body: &chunk},
			)
			if lastErr == nil {
				break
			}

			if attempt == rc.maxRetries {
				break
			}

			log.Warn().Err(lastErr).
				Int("chunk", i+1).Int("attempt", attempt+1).Dur("backoff", backoff).
				Msg("failed to post comment chunk, retrying")

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
				if backoff > rc.maxBackoff {
					backoff = rc.maxBackoff
				}
			}
		}
		if lastErr != nil {
			telemetry.SetError(span, lastErr, "Create comment chunk")
			log.Error().Err(lastErr).Int("chunk", i+1).Msg("failed to post comment chunk after retries")
			return fmt.Errorf("posting comment chunk %d of %d: %w", i+1, len(chunks), lastErr)
		}
		m.NoteID = int(*cc.ID)
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
