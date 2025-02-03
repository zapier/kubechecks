package github_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v62/github"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/shurcooL/githubv4"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

const MaxCommentLength = 64 * 1024

const sepEnd = "\n```\n</details>" +
	"\n<br>\n\n**Warning**: Output length greater than maximum allowed comment size. Continued in next comment."

const sepStart = "Continued from previous comment.\n<details><summary>Show Output</summary>\n\n"

// splitComment splits the given comment into chunks from the beginning,
// ensuring that each decorated chunk does not exceed maxSize.
// - The first chunk has no prefix but, if not the only chunk, is suffixed with sepEnd.
// - Subsequent chunks are prefixed with sepStart. If they’re not final, they are also suffixed with sepEnd.
// This forward‐splitting approach preserves the beginning of the comment.
func splitComment(comment string, maxSize int, sepEnd string, sepStart string) []string {
	// Guard: If the comment fits in one chunk, return it unsplit.
	if len(comment) <= maxSize {
		return []string{comment}
	}
	// Guard: if maxSize is too small to accommodate even one raw character with decorations,
	// return the unsplit comment.
	if maxSize < len(sepEnd)+1 || maxSize < len(sepStart)+1 {
		return []string{comment}
	}

	// Check if we have capacity for subsequent chunks
	if maxSize-len(sepStart)-len(sepEnd) <= 0 {
		// No room for raw text if we try to use both prefix and suffix
		// => fallback to unsplit
		return []string{comment}
	}

	var parts []string

	// Process the first chunk.
	// For the first chunk (if a split is needed) we reserve space for sepEnd only.
	firstRawCapacity := maxSize - len(sepEnd)
	firstChunkRaw := comment[0:firstRawCapacity]
	parts = append(parts, firstChunkRaw+sepEnd)
	i := firstRawCapacity

	// Process subsequent chunks.
	for i < len(comment) {
		remaining := len(comment) - i

		// If the remaining text fits in one final chunk (with only the sepStart prefix),
		// then create that final chunk without a trailing sepEnd.
		if remaining <= maxSize-len(sepStart) {
			parts = append(parts, sepStart+comment[i:])
			break
		} else {
			// Otherwise, for a non-final chunk, reserve space for both prefix and suffix.
			rawCapacity := maxSize - len(sepStart) - len(sepEnd)
			// The following slice is guaranteed to be in range because we only land here
			// if remaining > maxSize - len(sepStart). Consequently, rawCapacity <= remaining,
			// ensuring i+rawCapacity is within the comment's length.
			chunk := sepStart + comment[i:i+rawCapacity] + sepEnd
			parts = append(parts, chunk)
			i += rawCapacity
		}
	}
	return parts
}

func (c *Client) PostMessage(ctx context.Context, pr vcs.PullRequest, message string) (*msg.Message, error) {
	_, span := tracer.Start(ctx, "PostMessageToMergeRequest")
	defer span.End()

	if len(message) > MaxCommentLength {
		log.Warn().Int("original_length", len(message)).Msg("trimming the comment size")
		message = message[:MaxCommentLength]
	}

	if err := c.deleteLatestRunningComment(ctx, pr); err != nil {
		log.Error().Err(err).Msg("failed to delete latest 'kubechecks running' comment")
		return nil, err
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
		return nil, errors.Wrap(err, "could not post message to PR")
	}

	return msg.NewMessage(pr.FullName, pr.CheckID, int(*comment.ID), c), nil
}

func (c *Client) UpdateMessage(ctx context.Context, m *msg.Message, msg string) error {
	_, span := tracer.Start(ctx, "UpdateMessage")
	defer span.End()

	comments := splitComment(msg, MaxCommentLength, sepEnd, sepStart)

	owner, repo, ok := strings.Cut(m.Name, "/")
	if !ok {
		e := fmt.Errorf("invalid GitHub repository name: no '/' in %q", m.Name)
		telemetry.SetError(span, e, "Invalid GitHub full repository name")
		log.Error().Err(e).Msg("invalid GitHub repository name")
		return e
	}

	pr := vcs.PullRequest{
		Owner:    owner,
		Name:     repo,
		CheckID:  m.CheckID,
		FullName: m.Name,
	}

	log.Debug().Msgf("Updating message in PR %d in repo %s", pr.CheckID, pr.FullName)

	if err := c.deleteLatestRunningComment(ctx, pr); err != nil {
		return err
	}

	for _, comment := range comments {
		cc, _, err := c.googleClient.Issues.CreateComment(
			ctx, owner, repo, m.CheckID, &github.IssueComment{Body: &comment},
		)
		if err != nil {
			telemetry.SetError(span, err, "Update Pull Request comment")
			log.Error().Err(err).Msg("could not post updated message comment to PR")
			return err
		}
		m.NoteID = int(*cc.ID)
	}

	return nil
}

func (c *Client) deleteLatestRunningComment(ctx context.Context, pr vcs.PullRequest) error {
	_, span := tracer.Start(ctx, "deleteLatestRunningComment")
	defer span.End()

	existingComments, resp, err := c.googleClient.Issues.ListComments(
		ctx, pr.Owner, pr.Name, pr.CheckID, &github.IssueListCommentsOptions{
			Sort:      pkg.Pointer("created"),
			Direction: pkg.Pointer("asc"),
		},
	)
	if err != nil {
		telemetry.SetError(span, err, "List Pull Request comments")
		log.Error().Err(err).Msgf("could not retrieve existing PR comments, response: %+v", resp)
		return fmt.Errorf("failed to list comments: %w", err)
	}

	// Find and delete the first running comment.
	for _, existingComment := range existingComments {
		if existingComment.Body != nil && strings.Contains(*existingComment.Body, ":hourglass: kubechecks running ... ") {
			log.Debug().Msgf("Deleting 'kubechecks running' comment with ID %d", *existingComment.ID)
			if r, e := c.googleClient.Issues.DeleteComment(ctx, pr.Owner, pr.Name, *existingComment.ID); e != nil {
				telemetry.SetError(span, e, "Delete Pull Request comment")
				log.Error().Err(e).Msgf("failed to delete 'kubechecks running' comment, response: %+v", r)
				return fmt.Errorf("failed to delete 'kubechecks running' comment: %w", e)
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
			// GitHub API does not expose minimizeComment API. It's only available from the GraphQL API
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
