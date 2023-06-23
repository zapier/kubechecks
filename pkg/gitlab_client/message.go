package gitlab_client

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/xanzy/go-gitlab"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
	"github.com/zapier/kubechecks/telemetry"
	"go.opentelemetry.io/otel"
)

func (c *Client) PostMessage(ctx context.Context, projectName string, mergeRequestID int, msg string) *vcs_clients.Message {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "PostMessageToMergeRequest")
	defer span.End()

	n, _, err := c.Notes.CreateMergeRequestNote(
		projectName, mergeRequestID,
		&gitlab.CreateMergeRequestNoteOptions{
			Body: gitlab.String(msg),
		})
	if err != nil {
		telemetry.SetError(span, err, "Create Merge Request Note")
		log.Error().Err(err).Msg("could not post message to MR")
	}

	return &vcs_clients.Message{
		Lock:    sync.Mutex{},
		Name:    projectName,
		CheckID: mergeRequestID,
		NoteID:  n.ID,
		Msg:     msg,
		Client:  c,
	}
}

func (c *Client) CollapseExistingCheckCommentsInMergeRequest(ctx context.Context, projectId int, mergeRequestID int, lastCommitSHA string) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CollapseExistingCheckCommentsInMergeRequest")
	defer span.End()

	u, _, err := c.Users.CurrentUser()
	if err != nil {
		telemetry.SetError(span, err, "Collapse Existing Merge Request Check Notes")
		log.Error().Err(err).Int("projectId", projectId).Int("mr", mergeRequestID).Msg("could not fetch current user for Gitlab client")
	}

	// list merge request notes
	notes, _, err := c.Notes.ListMergeRequestNotes(projectId, mergeRequestID, &gitlab.ListMergeRequestNotesOptions{
		ListOptions: gitlab.ListOptions{
			Page:    1,
			PerPage: 100,
		},
	})
	if err != nil {
		telemetry.SetError(span, err, "Collapse Existing Merge Request Check Notes")
		log.Error().Err(err).Int("projectId", projectId).Int("mr", mergeRequestID).Msg("could not fetch notes for merge request")
	}

	// loop through notes and collapse any that are from the current user
	for _, note := range notes {
		if note.Author.ID != u.ID {
			continue
		}

		if strings.Contains(note.Body, fmt.Sprintf("<small>_Done. CommitSHA: %s_<small>\n", lastCommitSHA)) {
			continue
		}

		if !strings.Contains(note.Body, "<summary><i>OUTDATED: ArgoCD Application Checks: <code>") { // Check if the comment is already marked as outdated
			// collapse note Body
			appName := extractAppName(note.Body)
			if appName == "" {
				log.Debug().Int("projectId", projectId).Int("mr", mergeRequestID).Msgf("Could not extract app name from comment %d", note.ID)
				continue
			}

			log.Debug().Int("projectId", projectId).Int("mr", mergeRequestID).Msgf("Updating comment %d for %s app", note.ID, appName)
			newBody := fmt.Sprintf(`
<details>
	<summary><i>OUTDATED: ArgoCD Application Checks: <code>`+appName+`</code> </i> </summary>
	
%s
</details>
			`, note.Body)

			log.Debug().Int("projectId", projectId).Int("mr", mergeRequestID).Msgf("Updating comment %d as outdated", note.ID)

			_, _, err = c.Notes.UpdateMergeRequestNote(projectId, mergeRequestID, note.ID, &gitlab.UpdateMergeRequestNoteOptions{
				Body: &newBody,
			})
			if err != nil {
				telemetry.SetError(span, err, "Collapse Existing Merge Request Check Note")
				log.Error().Int("projectId", projectId).Int("mr", mergeRequestID).Err(err).Msgf("could not collapse note %d for merge request", note.ID)
				continue
			}
		}
	}
}

func extractAppName(input string) string {
	pattern := "## ArgoCD Application Checks: `([a-zA-Z0-9-_]+)` \n"
	// Compile the regex pattern
	regex := regexp.MustCompile(pattern)

	// Find the first match in the input string
	matches := regex.FindStringSubmatch(input)

	// Ensure a match is found and extract the application name
	if len(matches) == 2 {
		return matches[1]
	}

	return ""
}

func (c *Client) UpdateMessage(ctx context.Context, m *vcs_clients.Message, msg string) error {

	n, _, err := c.Notes.UpdateMergeRequestNote(m.Name, m.CheckID, m.NoteID, &gitlab.UpdateMergeRequestNoteOptions{
		Body: gitlab.String(m.Msg),
	})

	if err != nil {
		log.Error().Err(err).Msg("could not update message to MR")
		return err
	}

	// just incase the note ID changes
	m.NoteID = n.ID
	return nil
}
