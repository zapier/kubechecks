package gitlab_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/kubechecks/pkg/vcs"
)

// DiscussionsServices is the interface for GitLab discussions API.
type DiscussionsServices interface {
	CreateMergeRequestDiscussion(pid any, mergeRequest int, opt *gitlab.CreateMergeRequestDiscussionOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Discussion, *gitlab.Response, error)
	ListMergeRequestDiscussions(pid any, mergeRequest int, opt *gitlab.ListMergeRequestDiscussionsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Discussion, *gitlab.Response, error)
}

// DiscussionsService wraps the GitLab discussions service.
type DiscussionsService struct {
	DiscussionsServices
}

// PostReviewSuggestions posts MR discussions with inline code suggestions.
// Each suggestion is posted as a separate discussion on the specific file+line.
// Deduplicates against existing discussions to avoid posting the same suggestion twice.
func (c *Client) PostReviewSuggestions(ctx context.Context, pr vcs.PullRequest, _ string, suggestions []vcs.ReviewSuggestion) error {
	if len(suggestions) == 0 {
		return nil
	}

	// Get MR diff refs for position fields, DiffRefs arent available with the webhook MR requests payload,
	// this is the simplest way to find the Refs.
	mr, _, err := c.c.MergeRequests.GetMergeRequest(pr.FullName, pr.CheckID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to get merge request for diff refs: %w", err)
	}

	baseSHA := mr.DiffRefs.BaseSha
	headSHA := mr.DiffRefs.HeadSha
	startSHA := mr.DiffRefs.StartSha

	if baseSHA == "" || headSHA == "" || startSHA == "" {
		return fmt.Errorf("merge request diff refs incomplete (base=%s, head=%s, start=%s)", baseSHA, headSHA, startSHA)
	}

	// Fetch existing discussions for deduplication
	existing, err := c.listExistingDiscussionSuggestions(ctx, pr)
	if err != nil {
		log.Warn().Caller().Err(err).Msg("failed to list existing discussions, posting all suggestions")
	}

	posted := 0
	skipped := 0
	for _, s := range suggestions {
		// GitLab suggestion syntax: ```suggestion:-N+M where N=lines above, M=lines below
		// For single-line: -0+0 (replace just the target line)
		// For multi-line: -(endLine-startLine)+0 (replace from startLine to endLine)
		linesAbove := 0
		if s.StartLine > 0 && s.StartLine < s.EndLine {
			linesAbove = s.EndLine - s.StartLine
		}
		body := fmt.Sprintf("%s\n\n```suggestion:-%d+0\n%s\n```", s.Body, linesAbove, s.Suggestion)

		if isDuplicateGitLabSuggestion(existing, s.Path, s.EndLine, s.Suggestion) {
			log.Debug().Caller().
				Str("path", s.Path).
				Int("line", s.EndLine).
				Msg("skipping duplicate suggestion")
			skipped++
			continue
		}

		opts := &gitlab.CreateMergeRequestDiscussionOptions{
			Body: gitlab.Ptr(body),
			Position: &gitlab.PositionOptions{
				BaseSHA:      gitlab.Ptr(baseSHA),
				HeadSHA:      gitlab.Ptr(headSHA),
				StartSHA:     gitlab.Ptr(startSHA),
				NewPath:      gitlab.Ptr(s.Path),
				OldPath:      gitlab.Ptr(s.Path),
				PositionType: gitlab.Ptr("text"),
				NewLine:      gitlab.Ptr(s.EndLine),
			},
		}

		_, _, err := c.c.Discussions.CreateMergeRequestDiscussion(pr.FullName, pr.CheckID, opts, gitlab.WithContext(ctx))
		if err != nil {
			log.Warn().Caller().Err(err).
				Str("path", s.Path).
				Int("line", s.EndLine).
				Msg("failed to post suggestion discussion, skipping")
			continue
		}
		posted++
	}

	log.Info().
		Int("mr", pr.CheckID).
		Int("posted", posted).
		Int("skipped_duplicates", skipped).
		Int("total", len(suggestions)).
		Msg("posted GitLab review suggestions")

	return nil
}

// existingGitLabSuggestion is a minimal representation for deduplication.
type existingGitLabSuggestion struct {
	Path       string
	Line       int
	Suggestion string
}

// listExistingDiscussionSuggestions fetches existing discussions by the kubechecks user
// and extracts suggestion block content for deduplication.
func (c *Client) listExistingDiscussionSuggestions(ctx context.Context, pr vcs.PullRequest) ([]existingGitLabSuggestion, error) {
	var all []existingGitLabSuggestion
	opts := &gitlab.ListMergeRequestDiscussionsOptions{}

	for {
		discussions, resp, err := c.c.Discussions.ListMergeRequestDiscussions(pr.FullName, pr.CheckID, opts, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list discussions: %w", err)
		}

		for _, d := range discussions {
			for _, note := range d.Notes {
				if note.Author.Username != c.username {
					continue
				}
				suggestion := extractGitLabSuggestionBlock(note.Body)
				if suggestion == "" {
					continue
				}
				line := 0
				if note.Position != nil {
					line = note.Position.NewLine
				}
				path := ""
				if note.Position != nil {
					path = note.Position.NewPath
				}
				all = append(all, existingGitLabSuggestion{
					Path:       path,
					Line:       line,
					Suggestion: suggestion,
				})
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	log.Debug().Caller().
		Int("mr", pr.CheckID).
		Int("existing_suggestions", len(all)).
		Msg("fetched existing suggestion discussions by kubechecks")

	return all, nil
}

// isDuplicateGitLabSuggestion checks if a suggestion already exists.
// Matches on path + line + suggestion content (ignores explanation text).
func isDuplicateGitLabSuggestion(existing []existingGitLabSuggestion, path string, line int, suggestion string) bool {
	for _, e := range existing {
		if e.Path == path && e.Line == line && e.Suggestion == suggestion {
			return true
		}
	}
	return false
}

// extractGitLabSuggestionBlock extracts the content between ```suggestion and ``` markers.
// GitLab uses ```suggestion:-0+0 or just ```suggestion syntax.
func extractGitLabSuggestionBlock(body string) string {
	// Find start — GitLab suggestion blocks start with ```suggestion potentially followed by range
	startIdx := strings.Index(body, "```suggestion")
	if startIdx == -1 {
		return ""
	}
	// Find the newline after the opening marker
	nlIdx := strings.Index(body[startIdx:], "\n")
	if nlIdx == -1 {
		return ""
	}
	contentStart := startIdx + nlIdx + 1

	// Find the closing ```
	endIdx := strings.Index(body[contentStart:], "\n```")
	if endIdx == -1 {
		return ""
	}

	return body[contentStart : contentStart+endIdx]
}
