package gitlab_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/xanzy/go-gitlab"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/telemetry"
)

const MaxCommentLength = 1_000_000

func (c *Client) PostMessage(ctx context.Context, repo *repo.Repo, mergeRequestID int, msg string) *pkg.Message {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "PostMessageToMergeRequest")
	defer span.End()

	if len(msg) > MaxCommentLength {
		log.Warn().Int("original_length", len(msg)).Msg("trimming the comment size")
		msg = msg[:MaxCommentLength]
	}

	n, _, err := c.Notes.CreateMergeRequestNote(
		repo.FullName, mergeRequestID,
		&gitlab.CreateMergeRequestNoteOptions{
			Body: pkg.Pointer(msg),
		})
	if err != nil {
		telemetry.SetError(span, err, "Create Merge Request Note")
		log.Error().Err(err).Msg("could not post message to MR")
	}

	return pkg.NewMessage(repo.FullName, mergeRequestID, n.ID, c)
}

func (c *Client) hideOutdatedMessages(ctx context.Context, projectName string, mergeRequestID int, notes []*gitlab.Note) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "HideOutdatedMessages")
	defer span.End()

	log.Debug().Msg("hiding outdated comments")

	// loop through notes and collapse any that are from the current user
	for _, note := range notes {

		// Do not try to hide the note if
		// note user is not the gitlabTokenUser
		// note is an internal system note such as notes on commit messages
		// note is already hidden
		if note.Author.Username != c.username || note.System || strings.Contains(note.Body, "<summary><i>OUTDATED: Kubechecks Report</i></summary>") {
			continue
		}

		newBody := fmt.Sprintf(`
<details>
	<summary><i>OUTDATED: Kubechecks Report</i></summary>
	
%s
</details>
			`, note.Body)

		if len(newBody) > MaxCommentLength {
			log.Warn().Int("original_length", len(newBody)).Msg("trimming the comment size")
			newBody = newBody[:MaxCommentLength]
		}

		log.Debug().Str("projectName", projectName).Int("mr", mergeRequestID).Msgf("Updating comment %d as outdated", note.ID)

		_, _, err := c.Notes.UpdateMergeRequestNote(projectName, mergeRequestID, note.ID, &gitlab.UpdateMergeRequestNoteOptions{
			Body: &newBody,
		})

		if err != nil {
			telemetry.SetError(span, err, "Hide Existing Merge Request Check Note")
			return fmt.Errorf("could not hide note %d for merge request: %w", note.ID, err)
		}

	}
	return nil
}

func (c *Client) UpdateMessage(ctx context.Context, m *pkg.Message, msg string) error {
	log.Debug().Msgf("Updating message %d for %s", m.NoteID, m.Name)

	if len(msg) > MaxCommentLength {
		log.Warn().Int("original_length", len(msg)).Msg("trimming the comment size")
		msg = msg[:MaxCommentLength]
	}

	n, _, err := c.Notes.UpdateMergeRequestNote(m.Name, m.CheckID, m.NoteID, &gitlab.UpdateMergeRequestNoteOptions{
		Body: pkg.Pointer(msg),
	})

	if err != nil {
		log.Error().Err(err).Msg("could not update message to MR")
		return err
	}

	// just incase the note ID changes
	m.NoteID = n.ID
	return nil
}

// Iterate over all comments for the Merge Request, deleting any from the authenticated user
func (c *Client) pruneOldComments(ctx context.Context, projectName string, mrID int, notes []*gitlab.Note) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "pruneOldComments")
	defer span.End()

	log.Debug().Msg("deleting outdated comments")

	for _, note := range notes {
		if note.Author.Username == c.username {
			log.Debug().Int("mr", mrID).Int("note", note.ID).Msg("deleting old comment")
			_, err := c.Notes.DeleteMergeRequestNote(projectName, mrID, note.ID)
			if err != nil {
				telemetry.SetError(span, err, "Prune Old Comments")
				return fmt.Errorf("could not delete old comment: %w", err)
			}
		}
	}
	return nil
}

func (c *Client) TidyOutdatedComments(ctx context.Context, repo *repo.Repo) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "TidyOutdatedMessages")
	defer span.End()

	log.Debug().Msg("Tidying outdated comments")

	var allNotes []*gitlab.Note
	nextPage := 0

	for {
		// list merge request notes
		notes, resp, err := c.Notes.ListMergeRequestNotes(repo.FullName, repo.CheckID, &gitlab.ListMergeRequestNotesOptions{
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

	if strings.ToLower(viper.GetString("tidy-outdated-comments-mode")) == "delete" {
		return c.pruneOldComments(ctx, repo.FullName, repo.CheckID, allNotes)
	}
	return c.hideOutdatedMessages(ctx, repo.FullName, repo.CheckID, allNotes)

}
