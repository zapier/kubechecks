package gitlab_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/cenkalti/backoff/v5"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/xanzy/go-gitlab"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

const MaxCommentLength = 1_000_000

func (c *Client) PostMessage(ctx context.Context, pr vcs.PullRequest, message string) (*msg.Message, error) {
	_, span := tracer.Start(ctx, "PostMessage")
	defer span.End()

	exponentialBackOff := getBackOff()

	n, err := backoff.Retry(ctx, func() (*gitlab.Note, error) {
		n, _, err := c.c.Notes.CreateMergeRequestNote(
			pr.FullName, pr.CheckID,
			&gitlab.CreateMergeRequestNoteOptions{
				Body: pkg.Pointer(message),
			})
		return n, err
	}, backoff.WithBackOff(exponentialBackOff))

	if err != nil {
		telemetry.SetError(span, err, "Create Merge Request Note")
		return nil, errors.Wrap(err, "could not post message to MR")
	}

	return msg.NewMessage(pr.FullName, pr.CheckID, n.ID, c), nil
}

func (c *Client) hideOutdatedMessages(ctx context.Context, projectName string, mergeRequestID int, notes []*gitlab.Note) error {
	_, span := tracer.Start(ctx, "HideOutdatedMessages")
	defer span.End()

	log.Debug().Msg("hiding outdated comments")

	// loop through notes and collapse any that are from the current user and current identifier
	for _, note := range notes {

		// Do not try to hide the note if
		// note user is not the gitlabTokenUser
		// note is an internal system note such as notes on commit messages
		// note is already hidden
		if note.Author.Username != c.username || note.System ||
			strings.Contains(note.Body, fmt.Sprintf("<summary><i>OUTDATED: %s</i></summary>", pkg.GetMessageHeader(c.cfg.Identifier))) ||
			!strings.Contains(note.Body, pkg.GetMessageHeader(c.cfg.Identifier)) {
			continue
		}

		newBody := fmt.Sprintf(`
<details>
	<summary><i>OUTDATED: %s</i></summary>
	
%s
</details>
			`, pkg.GetMessageHeader(c.cfg.Identifier), note.Body)

		log.Debug().Str("projectName", projectName).Int("mr", mergeRequestID).Msgf("Updating comment %d as outdated", note.ID)

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

func (c *Client) UpdateMessage(ctx context.Context, pr vcs.PullRequest, m *msg.Message, messages []string) error {
	log.Debug().Msgf("Updating message %d for %s", m.NoteID, m.Name)

	for i, msg := range messages {
		if i == 0 {
			exponentialBackOff := getBackOff()

			n, err := backoff.Retry(ctx, func() (*gitlab.Note, error) {
				n, _, err := c.c.Notes.UpdateMergeRequestNote(m.Name, m.CheckID, m.NoteID, &gitlab.UpdateMergeRequestNoteOptions{
					Body: pkg.Pointer(msg),
				})
				return n, err
			}, backoff.WithBackOff(exponentialBackOff))

			if err != nil {
				log.Error().Err(err).Msg("could not update message to MR")
				return err
			}
			// just incase the note ID changes
			m.NoteID = n.ID
		} else {
			continuedHeader := fmt.Sprintf(
				"> Continued from previous [comment](%s)\n",
				fmt.Sprintf("https://gitlab.com/%s/%s/merge_requests/%d#note_%d", pr.Owner, pr.Name, pr.CheckID, m.NoteID),
			)

			msg = fmt.Sprintf("%s\n\n%s", continuedHeader, msg)
			n, err := c.PostMessage(ctx, pr, msg)
			if err != nil {
				log.Error().Err(err).Msg("could not post message to MR")
				return err
			}
			m.NoteID = n.NoteID
		}

	}
	return nil
}

// Iterate over all comments for the Merge Request, deleting any from the authenticated user
func (c *Client) pruneOldComments(ctx context.Context, projectName string, mrID int, notes []*gitlab.Note) error {
	_, span := tracer.Start(ctx, "pruneOldComments")
	defer span.End()

	log.Debug().Msg("deleting outdated comments")

	for _, note := range notes {
		if note.Author.Username == c.username && strings.Contains(note.Body, pkg.GetMessageHeader(c.cfg.Identifier)) {
			log.Debug().Int("mr", mrID).Int("note", note.ID).Msg("deleting old comment")
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

	log.Debug().Msg("Tidying outdated comments")

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

func (c *Client) GetMaxCommentLength() int {
	return MaxCommentLength
}

func (c *Client) GetPrCommentLinkTemplate(pr vcs.PullRequest) string {
	return fmt.Sprintf("https://gitlab.com/%s/%s/merge_requests/%d#note_0000000000", pr.Owner, pr.Name, pr.CheckID)
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
