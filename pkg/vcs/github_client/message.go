package github_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v62/github"
	"github.com/rs/zerolog/log"
	"github.com/shurcooL/githubv4"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

const MaxCommentLength = 64 * 1024

const sepEnd = "\n```\n</details>" +
"\n<br>\n\n**Warning**: Output length greater than max comment size. Continued in next comment."

const sepStart = "Continued from previous comment.\n<details><summary>Show Output</summary>\n\n"

// SplitComment splits comment into a slice of comments that are under maxSize.
// It appends sepEnd to all comments that have a following comment.
// It prepends sepStart to all comments that have a preceding comment.
func SplitComment(comment string, maxSize int, sepEnd string, sepStart string) []string {
	if len(comment) <= maxSize {
		return []string{comment}
	}

	var comments []string
	var builder strings.Builder
	remaining := comment
	maxWithSep := maxSize - len(sepEnd) - len(sepStart)
	sepStartWithCode := sepStart + "```diff\n"

	for len(remaining) > 0 {
		if builder.Len() > 0 && builder.Len()+len(sepEnd) > maxWithSep {
			comments = append(comments, builder.String()+sepEnd)
			builder.Reset()
			builder.WriteString(sepStartWithCode)
		}

		lineEnd := strings.LastIndex(remaining[:min(len(remaining), maxWithSep-builder.Len())], "\n")
		if lineEnd == -1 {
			lineEnd = min(len(remaining), maxWithSep-builder.Len())
		} else {
			lineEnd++
		}

		builder.WriteString(remaining[:lineEnd])
		remaining = remaining[lineEnd:]

		if builder.Len() >= maxWithSep {
			comments = append(comments, builder.String()+sepEnd)
			builder.Reset()
			builder.WriteString(sepStartWithCode)
		}
	}

	if builder.Len() > 0 {
		comments = append(comments, builder.String())
	}

	return comments
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (c *Client) PostMessage(ctx context.Context, pr vcs.PullRequest, message string) *msg.Message {
	_, span := tracer.Start(ctx, "PostMessageToMergeRequest")
	defer span.End()

	if len(message) > MaxCommentLength {
		log.Warn().Int("original_length", len(message)).Msg("trimming the comment size")
		message = message[:MaxCommentLength]
	}

	if err := c.deleteLatestRunningComment(ctx, pr); err != nil {
		log.Error().Err(err).Msg("failed to delete latest 'kubechecks running' comment")
		return nil
	}

	log.Debug().Msgf("Posting message to PR %d in repo %s", pr.CheckID, pr.FullName)
	comment, _, err := c.googleClient.Issues.CreateComment(
		ctx,
		pr.Owner,
		pr.Name,
		pr.CheckID,
		&github.IssueComment{Body: &message},
	)

	if err != nil {
		telemetry.SetError(span, err, "Create Pull Request comment")
		log.Error().Err(err).Msg("could not post message to PR")
	}

	return msg.NewMessage(pr.FullName, pr.CheckID, int(*comment.ID), c)
}

func (c *Client) UpdateMessage(ctx context.Context, m *msg.Message, message string) error {
	_, span := tracer.Start(ctx, "UpdateMessage")
	defer span.End()

	comments := SplitComment(message, MaxCommentLength, sepEnd, sepStart)
	repoNameComponents := strings.Split(m.Name, "/")

	pr := vcs.PullRequest{
		Owner:   repoNameComponents[0],
		Name:    repoNameComponents[1],
		CheckID: m.CheckID,
		FullName: fmt.Sprintf("%s/%s", repoNameComponents[0], repoNameComponents[1]),
	}

	log.Debug().Msgf("Updating message in PR %d in repo %s", pr.CheckID, pr.FullName)

	if err := c.deleteLatestRunningComment(ctx, pr); err != nil {
		return err
	}

	for _, comment := range comments {
		comment, _, err := c.googleClient.Issues.CreateComment(
			ctx,
			repoNameComponents[0],
			repoNameComponents[1],
			m.CheckID,
			&github.IssueComment{Body: &comment},
		)
		if err != nil {
			telemetry.SetError(span, err, "Update Pull Request comment")
			log.Error().Err(err).Msg("could not post updated message comment to PR")
			return err
		}
		m.NoteID = int(*comment.ID)
	}

	return nil
}

func (c *Client) deleteLatestRunningComment(ctx context.Context, pr vcs.PullRequest) error {
	_, span := tracer.Start(ctx, "deleteLatestRunningComment")
	repoNameComponents := strings.Split(pr.FullName, "/")

	existingComments, _, err := c.googleClient.Issues.ListComments(ctx, repoNameComponents[0], repoNameComponents[1], pr.CheckID, &github.IssueListCommentsOptions{
		Sort:      pkg.Pointer("created"),
		Direction: pkg.Pointer("asc"),
	})
	if err != nil {
		telemetry.SetError(span, err, "List Pull Request comments")
		log.Error().Err(err).Msg("could not retrieve existing comments for PR")
		return fmt.Errorf("failed to list comments: %w", err)
	}

	for _, existingComment := range existingComments {
		if existingComment.Body != nil && strings.Contains(*existingComment.Body, ":hourglass: kubechecks running ... ") {
			log.Debug().Msgf("Deleting 'kubechecks running' comment with ID %d", *existingComment.ID)
			if _, err := c.googleClient.Issues.DeleteComment(ctx, repoNameComponents[0], repoNameComponents[1], *existingComment.ID); err != nil {
				telemetry.SetError(span, err, "Delete Pull Request comment")
				log.Error().Err(err).Msg("failed to delete 'kubechecks running' comment")
				return fmt.Errorf("failed to delete 'kubechecks running' comment: %w", err)
			}
			break
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

	log.Debug().Msgf("Pruning messages from PR %d in repo %s", pr.CheckID, pr.FullName)

	for _, comment := range comments {
		if strings.EqualFold(comment.GetUser().GetLogin(), c.username) {
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
		if strings.EqualFold(comment.GetUser().GetLogin(), c.username) {
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
