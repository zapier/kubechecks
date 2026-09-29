package gitlab_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

const MaxCommentLength = 1_000_000

func (c *Client) MaxCommentLength() int { return MaxCommentLength }

func (c *Client) PostMessage(ctx context.Context, pr vcs.PullRequest, message string) (*msg.Message, error) {
	_, span := tracer.Start(ctx, "PostMessage")
	defer span.End()

	if len(message) > MaxCommentLength {
		log.Warn().Int("original_length", len(message)).Msg("trimming the comment size")
		message = message[:MaxCommentLength]
	}

	n, _, err := c.c.Notes.CreateMergeRequestNote(
		pr.FullName, int64(pr.CheckID),
		&gitlab.CreateMergeRequestNoteOptions{
			Body: pkg.Pointer(message),
		})
	if err != nil {
		telemetry.SetError(span, err, "Create Merge Request Note")
		return nil, errors.Wrap(err, "could not post message to MR")
	}

	return msg.NewMessage(pr.FullName, pr.CheckID, int(n.ID), c), nil
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

		if len(newBody) > MaxCommentLength {
			log.Warn().Int("original_length", len(newBody)).Msg("trimming the comment size")
			newBody = newBody[:MaxCommentLength]
		}

		log.Debug().Caller().Str("projectName", projectName).Int("mr", mergeRequestID).Msgf("Updating comment %d as outdated", note.ID)

		_, _, err := c.c.Notes.UpdateMergeRequestNote(projectName, int64(mergeRequestID), note.ID, &gitlab.UpdateMergeRequestNoteOptions{
			Body: &newBody,
		})

		if err != nil {
			telemetry.SetError(span, err, "Hide Existing Merge Request Check Note")
			return fmt.Errorf("could not hide note %d for merge request: %w", note.ID, err)
		}
	}

	return nil
}

func (c *Client) UpdateMessage(ctx context.Context, pr vcs.PullRequest, noteID int, chunks []string) error {
	log.Debug().Caller().Int("chunks", len(chunks)).Msgf("Updating message %d for %s", noteID, pr.FullName)

	// the first chunk replaces the placeholder note, every other chunk is a new note
	for i, chunk := range chunks {
		if len(chunk) > MaxCommentLength {
			log.Warn().Int("original_length", len(chunk)).Msg("trimming the comment size")
			chunk = chunk[:MaxCommentLength]
		}

		var err error
		if i == 0 {
			err = c.editNote(ctx, pr, noteID, chunk)
		} else {
			err = c.addNote(ctx, pr, chunk)
		}
		if err != nil {
			log.Error().Err(err).Int("chunk", i+1).Msg("could not update message to MR")
			return fmt.Errorf("posting note %d of %d: %w", i+1, len(chunks), err)
		}
	}

	return nil
}

func (c *Client) editNote(ctx context.Context, pr vcs.PullRequest, noteID int, body string) error {
	_, _, err := c.c.Notes.UpdateMergeRequestNote(pr.FullName, int64(pr.CheckID), int64(noteID),
		&gitlab.UpdateMergeRequestNoteOptions{Body: pkg.Pointer(body)}, gitlab.WithContext(ctx))
	return err
}

// addNote posts one part as a new note. The gitlab client retries 429 and 5xx
// on its own, so a 5xx that came back after GitLab had saved the note leaves
// that part on the MR twice. Rare and harmless, nothing guards against it.
func (c *Client) addNote(ctx context.Context, pr vcs.PullRequest, body string) error {
	_, _, err := c.c.Notes.CreateMergeRequestNote(pr.FullName, int64(pr.CheckID),
		&gitlab.CreateMergeRequestNoteOptions{Body: pkg.Pointer(body)}, gitlab.WithContext(ctx))
	return err
}

// Iterate over all comments for the Merge Request, deleting any from the authenticated user
func (c *Client) pruneOldComments(ctx context.Context, projectName string, mrID int, notes []*gitlab.Note) error {
	_, span := tracer.Start(ctx, "pruneOldComments")
	defer span.End()

	log.Debug().Caller().Str("projectName", projectName).Int("mr", mrID).Msg("deleting outdated comments")

	for _, note := range notes {
		if note.Author.Username == c.username && strings.Contains(note.Body, fmt.Sprintf("Kubechecks %s Report", c.cfg.Identifier)) {
			log.Debug().Caller().Int("mr", mrID).Int64("note", note.ID).Msg("deleting old comment")
			_, err := c.c.Notes.DeleteMergeRequestNote(projectName, int64(mrID), note.ID)
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
	var nextPage int64

	for {
		// list merge request notes
		notes, resp, err := c.c.Notes.ListMergeRequestNotes(pr.FullName, int64(pr.CheckID), &gitlab.ListMergeRequestNotesOptions{
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
	CreateMergeRequestNote(pid interface{}, mergeRequest int64, opt *gitlab.CreateMergeRequestNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error)
	UpdateMergeRequestNote(pid interface{}, mergeRequest, note int64, opt *gitlab.UpdateMergeRequestNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error)
	DeleteMergeRequestNote(pid interface{}, mergeRequest, note int64, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error)
	ListMergeRequestNotes(pid interface{}, mergeRequest int64, opt *gitlab.ListMergeRequestNotesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Note, *gitlab.Response, error)
}

type NotesService struct {
	NotesServices
}
