package github_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v74/github"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/vcs"
)

// PostReviewSuggestions posts a PR review with inline code suggestions.
// Deduplicates against existing review comments to avoid posting the same suggestion twice.
func (c *Client) PostReviewSuggestions(ctx context.Context, pr vcs.PullRequest, summary string, suggestions []vcs.ReviewSuggestion) error {
	if len(suggestions) == 0 {
		return nil
	}

	// Fetch existing review comments to deduplicate
	existing, err := c.listExistingReviewComments(ctx, pr)
	if err != nil {
		log.Warn().Caller().Err(err).Msg("failed to list existing review comments, posting all suggestions")
	}

	var comments []*github.DraftReviewComment
	for _, s := range suggestions {
		body := s.Body + "\n\n```suggestion\n" + s.Suggestion + "\n```"

		if isDuplicateSuggestion(existing, s.Path, s.EndLine, s.Suggestion) {
			log.Debug().Caller().
				Str("path", s.Path).
				Int("line", s.EndLine).
				Msg("skipping duplicate suggestion")
			continue
		}

		side := "RIGHT"
		comment := &github.DraftReviewComment{
			Path: &s.Path,
			Body: &body,
			Side: &side,
			Line: &s.EndLine,
		}

		if s.StartLine > 0 && s.StartLine < s.EndLine {
			comment.StartLine = &s.StartLine
			comment.StartSide = &side
		}

		comments = append(comments, comment)
	}

	if len(comments) == 0 {
		log.Debug().Caller().Int("pr", pr.CheckID).Msg("all suggestions already exist, skipping review post")
		return nil
	}

	log.Debug().Caller().
		Int("pr", pr.CheckID).
		Int("new_suggestions", len(comments)).
		Int("total_suggestions", len(suggestions)).
		Int("skipped_duplicates", len(suggestions)-len(comments)).
		Msg("posting review with suggestions")

	event := "COMMENT"
	review := &github.PullRequestReviewRequest{
		CommitID: &pr.SHA,
		Event:    &event,
		Body:     pkg.Pointer(summary),
		Comments: comments,
	}

	_, _, err = c.googleClient.PullRequests.CreateReview(ctx, pr.Owner, pr.Name, pr.CheckID, review)
	if err != nil {
		return fmt.Errorf("failed to create review with suggestions: %w", err)
	}

	log.Info().
		Int("pr", pr.CheckID).
		Int("suggestions", len(comments)).
		Msg("posted review with suggestions")

	return nil
}

// existingComment is a minimal representation of an existing PR review comment for deduplication.
type existingComment struct {
	Path       string
	Line       int
	Suggestion string // extracted from ```suggestion block
}

// listExistingReviewComments fetches all review comments on the PR made by kubechecks.
// Extracts the suggestion block content for deduplication.
func (c *Client) listExistingReviewComments(ctx context.Context, pr vcs.PullRequest) ([]existingComment, error) {
	var all []existingComment
	opts := &github.PullRequestListCommentsOptions{
		Sort:      "created",
		Direction: "desc",
	}

	for {
		comments, resp, err := c.googleClient.PullRequests.ListComments(ctx, pr.Owner, pr.Name, pr.CheckID, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list review comments: %w", err)
		}

		for _, comment := range comments {
			if !strings.EqualFold(comment.GetUser().GetLogin(), c.username) {
				continue
			}
			suggestion := extractSuggestionBlock(comment.GetBody())
			if suggestion == "" {
				continue // not a suggestion comment
			}
			all = append(all, existingComment{
				Path:       comment.GetPath(),
				Line:       comment.GetLine(),
				Suggestion: suggestion,
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}

	log.Debug().Caller().
		Int("pr", pr.CheckID).
		Int("existing_suggestions", len(all)).
		Msg("fetched existing suggestion comments by kubechecks")

	return all, nil
}

// isDuplicateSuggestion checks if a suggestion already exists in the PR's review comments.
// Matches on path + line + suggestion content only (ignores explanation text).
func isDuplicateSuggestion(existing []existingComment, path string, line int, suggestion string) bool {
	for _, e := range existing {
		if e.Path == path && e.Line == line && e.Suggestion == suggestion {
			return true
		}
	}
	return false
}

// extractSuggestionBlock extracts the content between ```suggestion and ``` markers.
func extractSuggestionBlock(body string) string {
	const startMarker = "```suggestion\n"
	const endMarker = "\n```"

	startIdx := strings.Index(body, startMarker)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(startMarker)

	endIdx := strings.Index(body[startIdx:], endMarker)
	if endIdx == -1 {
		return ""
	}

	return body[startIdx : startIdx+endIdx]
}
