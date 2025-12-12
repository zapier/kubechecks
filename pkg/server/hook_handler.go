package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/events"
	"github.com/zapier/kubechecks/pkg/queue"
	"github.com/zapier/kubechecks/pkg/vcs"
)

var tracer = otel.Tracer("pkg/server")

type VCSHookHandler struct {
	ctr          container.Container
	processors   []checks.ProcessorEntry
	queueManager *queue.QueueManager
}

func NewVCSHookHandler(ctr container.Container, processors []checks.ProcessorEntry, queueManager *queue.QueueManager) *VCSHookHandler {
	return &VCSHookHandler{
		ctr:          ctr,
		processors:   processors,
		queueManager: queueManager,
	}
}
func (h *VCSHookHandler) AttachHandlers(grp *echo.Group) {
	projectHookPath := fmt.Sprintf("/%s/project", h.ctr.VcsClient.GetName())
	grp.POST(projectHookPath, h.groupHandler)
}

func (h *VCSHookHandler) groupHandler(c echo.Context) error {
	ctx := context.Background()
	log.Debug().Msg("Received hook request")
	// Always verify, even if no secret (no op if no secret)
	payload, err := h.ctr.VcsClient.VerifyHook(c.Request(), h.ctr.Config.WebhookSecret)
	if err != nil {
		log.Err(err).Msg("Failed to verify hook")
		return c.String(http.StatusUnauthorized, "Unauthorized")
	}

	pr, err := h.ctr.VcsClient.ParseHook(ctx, c.Request(), payload)
	if err != nil {
		switch err {
		case vcs.ErrInvalidType:
			log.Debug().Msg("Ignoring event, not a supported request")
			return c.String(http.StatusOK, "Skipped")
		default:
			// TODO: do something ELSE with the error
			log.Error().Err(err).Msg("Failed to create a repository locally")
			return echo.ErrBadRequest
		}
	}

	// Check label filter before enqueueing
	if !h.passesLabelFilter(pr) {
		log.Warn().
			Str("repo", pr.CloneURL).
			Int("check_id", pr.CheckID).
			Str("label-filter", h.ctr.Config.LabelFilter).
			Msg("ignoring event, did not have matching label")
		return c.String(http.StatusOK, "Skipped - label filter")
	}

	// Enqueue the check request (will be processed by worker)
	if err := h.queueManager.Enqueue(ctx, queue.EnqueueParams{
		PullRequest: pr,
		Container:   h.ctr,
		Processors:  h.processors,
	}); err != nil {
		// Queue is full - notify user via VCS comment
		log.Warn().
			Err(err).
			Str("repo", pr.CloneURL).
			Int("check_id", pr.CheckID).
			Msg("queue full, notifying user via VCS")

		message := fmt.Sprintf("⚠️ Kubechecks worker is currently busy processing other requests.\n\n"+
			"The queue for this repository is full. Please try again later by commenting `%s`.",
			h.ctr.Config.ReplanCommentMessage)

		// Post message to VCS (non-blocking, best effort)
		if _, postErr := h.ctr.VcsClient.PostMessage(ctx, pr, message); postErr != nil {
			log.Error().
				Err(postErr).
				Str("repo", pr.CloneURL).
				Int("check_id", pr.CheckID).
				Msg("failed to post queue-full message to VCS")
		}

		// Still return 200 OK to webhook caller
		return c.String(http.StatusOK, "Queue full - user notified")
	}

	return c.String(http.StatusOK, "Accepted")
}

type RepoDirectory struct {
}

func ProcessCheckEvent(ctx context.Context, pr vcs.PullRequest, ctr container.Container, processors []checks.ProcessorEntry) {
	ctx, span := tracer.Start(ctx, "processCheckEvent",
		trace.WithAttributes(
			attribute.Int("mr_id", pr.CheckID),
			attribute.String("project", pr.Name),
			attribute.String("sha", pr.SHA),
			attribute.String("source", pr.HeadRef),
			attribute.String("target", pr.BaseRef),
			attribute.String("default_branch", pr.DefaultBranch),
		),
	)
	defer span.End()

	// Use container's repo manager
	repoMgr := ctr.RepoManager
	defer repoMgr.Cleanup()

	// If we've gotten here, we can now begin running checks (or trying to)
	cEvent := events.NewCheckEvent(pr, ctr, repoMgr, processors)
	if err := cEvent.Process(ctx); err != nil {
		span.RecordError(err)
		log.Error().Err(err).Msg("failed to process the request")
	}
}

// passesLabelFilter checks if the given mergeEvent has a label that starts with "kubechecks:"
// and matches the handler's labelFilter. Returns true if there's a matching label or no
// "kubechecks:" labels are found, and false if a "kubechecks:" label is found but none match
// the labelFilter.
func (h *VCSHookHandler) passesLabelFilter(repo vcs.PullRequest) bool {
	foundKubechecksLabel := false

	for _, label := range repo.Labels {
		log.Debug().Str("check_label", label).Msg("checking label for match")
		// Check if label starts with "kubechecks:"
		if strings.HasPrefix(label, "kubechecks:") {
			foundKubechecksLabel = true

			// Get the remaining string after "kubechecks:"
			remainingString := strings.TrimPrefix(label, "kubechecks:")
			if remainingString == h.ctr.Config.LabelFilter {
				log.Debug().Str("mr_label", label).Msg("label is match for our filter")
				return true
			}
		}
	}

	// Return false if kubechecks label was found but did not match labelFilter, else return true
	if foundKubechecksLabel {
		return false
	}

	// Return false if we have a label filter, but it did not match any labels on the event
	if h.ctr.Config.LabelFilter != "" {
		return false
	}

	return true
}
