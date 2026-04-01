package gitlab_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

const maxCommentLength = 1_000_000

func (c *Client) MaxCommentLength() int { return maxCommentLength }

func (c *Client) PostMessage(ctx context.Context, pr vcs.PullRequest, message string) (*msg.Message, error) {
	_, span := tracer.Start(ctx, "PostMessage")
	defer span.End()

	if len(message) > maxCommentLength {
		telemetry.SetError(span, fmt.Errorf("message length %d exceeds limit %d", len(message), maxCommentLength), "PostMessage")
		return nil, fmt.Errorf("message length %d exceeds GitLab comment limit %d", len(message), maxCommentLength)
	}

	n, _, err := c.c.Notes.CreateMergeRequestNote(
		pr.FullName, pr.CheckID,
		&gitlab.CreateMergeRequestNoteOptions{
			Body: pkg.Pointer(message),
		})
	if err != nil {
		telemetry.SetError(span, err, "Create Merge Request Note")
		return nil, errors.Wrap(err, "could not post message to MR")
	}

	return msg.NewMessage(pr.FullName, pr.CheckID, n.ID, c), nil
}

func (c *Client) hideOutdatedMessages(ctx context.Context, projectName string, mergeRequestID int, notes []*gitlab.Note) error {
	_, span := tracer.Start(ctx, "HideOutdatedMessages")
	defer span.End()

	log.Debug().Caller().Str("projectName", projectName).Int("mr", mergeRequestID).Msg("hiding outdated comments")

	// loop through notes and collapse any that are from the current user and current identifier
	for _, note := range notes {

		// Do not try to hide the note if
		// note user is not the gitlabTokenUser
		// note is an internal system note such as notes on commit messages
		// note is already hidden
		if note.Author.Username != c.username || note.System ||
			strings.Contains(note.Body, fmt.Sprintf("<summary><i>OUTDATED: Kubechecks %s Report</i></summary>", c.cfg.Identifier)) ||
			!strings.Contains(note.Body, fmt.Sprintf("Kubechecks %s Report", c.cfg.Identifier)) {
			continue
		}

		newBody := fmt.Sprintf(`
<details>
	<summary><i>OUTDATED: Kubechecks %s Report</i></summary>
	
%s
</details>
			`, c.cfg.Identifier, note.Body)

		if len(newBody) > maxCommentLength {
			log.Warn().Int("original_length", len(newBody)).Msg("trimming the comment size")
			newBody = newBody[:maxCommentLength]
		}

		log.Debug().Caller().Str("projectName", projectName).Int("mr", mergeRequestID).Msgf("Updating comment %d as outdated", note.ID)

		_, _, err := c.c.Notes.UpdateMergeRequestNote(projectName, mergeRequestID, note.ID, &gitlab.UpdateMergeRequestNoteOptions{
			Body: &newBody,
		})

		if err != nil {
			telemetry.SetError(span, err, "Hide Existing Merge Request Check Note")
			return fmt.Errorf("could not hide note %d for merge request: %w", note.ID, err)
		}
	}

	return nil
}

func (c *Client) UpdateMessage(ctx context.Context, pr vcs.PullRequest, m *msg.Message, chunks []string) error {
	log.Debug().Caller().Msgf("Deleting placeholder note %d for MR %d in %s", m.NoteID, pr.CheckID, pr.FullName)
	if _, err := c.c.Notes.DeleteMergeRequestNote(pr.FullName, pr.CheckID, m.NoteID); err != nil {
		log.Error().Err(err).Msg("failed to delete placeholder note")
		return fmt.Errorf("deleting placeholder note: %w", err)
	}

	log.Info().Int("chunks", len(chunks)).Msgf("Posting %d note(s) to MR %d in %s", len(chunks), pr.CheckID, pr.FullName)
	for i, chunk := range chunks {
		var note *gitlab.Note
		err := backoff.Retry(func() error {
			var resp *gitlab.Response
			var createErr error
			note, resp, createErr = c.c.Notes.CreateMergeRequestNote(
				pr.FullName, pr.CheckID,
				&gitlab.CreateMergeRequestNoteOptions{
					Body: pkg.Pointer(chunk),
				},
				gitlab.WithContext(ctx),
			)
			return checkReturnForBackoff(resp, createErr)
		}, getBackOff())
		if err != nil {
			log.Error().Err(err).Int("chunk", i+1).Msg("failed to post note chunk after retries")
			return fmt.Errorf("posting note chunk %d of %d: %w", i+1, len(chunks), err)
		}
		m.NoteID = note.ID
	}

	return nil
}

// Iterate over all comments for the Merge Request, deleting any from the authenticated user
func (c *Client) pruneOldComments(ctx context.Context, projectName string, mrID int, notes []*gitlab.Note) error {
	_, span := tracer.Start(ctx, "pruneOldComments")
	defer span.End()

	log.Debug().Caller().Str("projectName", projectName).Int("mr", mrID).Msg("deleting outdated comments")

	for _, note := range notes {
		if note.Author.Username == c.username && strings.Contains(note.Body, fmt.Sprintf("Kubechecks %s Report", c.cfg.Identifier)) {
			log.Debug().Caller().Int("mr", mrID).Int("note", note.ID).Msg("deleting old comment")
			_, err := c.c.Notes.DeleteMergeRequestNote(projectName, mrID, note.ID)
			if err != nil {
				telemetry.SetError(span, err, "Prune Old Comments")
				return fmt.Errorf("could not delete old comment: %w", err)
			}
		}
	}
	return nil
}

func (c *Client) TidyOutdatedComments(ctx context.Context, pr vcs.PullRequest) error {
	_, span := tracer.Start(ctx, "TidyOutdatedMessages")
	defer span.End()

	log.Debug().Caller().Msg("Tidying outdated comments")

	var allNotes []*gitlab.Note
	nextPage := 0

	for {
		// list merge request notes
		notes, resp, err := c.c.Notes.ListMergeRequestNotes(pr.FullName, pr.CheckID, &gitlab.ListMergeRequestNotesOptions{
			Sort:    pkg.Pointer("asc"),
			OrderBy: pkg.Pointer("created_at"),
			ListOptions: gitlab.ListOptions{
				Page: nextPage,
			},
		})

		if err != nil {
			telemetry.SetError(span, err, "Tidy Outdated Comments")
			return fmt.Errorf("could not fetch notes for merge request: %w", err)
		}
		allNotes = append(allNotes, notes...)
		if resp.NextPage == 0 {
			break
		}
		nextPage = resp.NextPage
	}

	if strings.ToLower(c.cfg.TidyOutdatedCommentsMode) == "delete" {
		return c.pruneOldComments(ctx, pr.FullName, pr.CheckID, allNotes)
	}
	return c.hideOutdatedMessages(ctx, pr.FullName, pr.CheckID, allNotes)

}

type NotesServices interface {
	CreateMergeRequestNote(pid interface{}, mergeRequest int, opt *gitlab.CreateMergeRequestNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error)
	UpdateMergeRequestNote(pid interface{}, mergeRequest, note int, opt *gitlab.UpdateMergeRequestNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error)
	DeleteMergeRequestNote(pid interface{}, mergeRequest, note int, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error)
	ListMergeRequestNotes(pid interface{}, mergeRequest int, opt *gitlab.ListMergeRequestNotesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Note, *gitlab.Response, error)
}

type NotesService struct {
	NotesServices
}
