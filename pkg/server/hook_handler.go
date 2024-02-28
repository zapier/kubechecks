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

	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/events"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

type VCSHookHandler struct {
	ctr container.Container
}

func NewVCSHookHandler(ctr container.Container) *VCSHookHandler {
	return &VCSHookHandler{
		ctr: ctr,
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

	r, err := h.ctr.VcsClient.ParseHook(c.Request(), payload)
	if err != nil {
		switch err {
		case vcs.ErrInvalidType:
			log.Debug().Msg("Ignoring event, not a merge request")
			return c.String(http.StatusOK, "Skipped")
		default:
			// TODO: do something ELSE with the error
			log.Error().Err(err).Msg("Failed to create a repository locally")
			return echo.ErrBadRequest
		}
	}

	// We now have a generic repo with all the info we need to start processing an event. Hand off to the event processor
	go h.processCheckEvent(ctx, r)
	return c.String(http.StatusAccepted, "Accepted")
}

// Takes a constructed Repo, and attempts to run the Kubechecks processing suite against it.
// If the Repo is not yet populated, this will fail.
func (h *VCSHookHandler) processCheckEvent(ctx context.Context, repo *vcs.Repo) {
	if !h.passesLabelFilter(repo) {
		log.Warn().Str("label-filter", h.ctr.Config.LabelFilter).Msg("ignoring event, did not have matching label")
		return
	}

	ProcessCheckEvent(ctx, repo, h.ctr)
}

func ProcessCheckEvent(ctx context.Context, r *vcs.Repo, ctr container.Container) {
	var span trace.Span
	ctx, span = otel.Tracer("Kubechecks").Start(ctx, "processCheckEvent",
		trace.WithAttributes(
			attribute.Int("mr_id", r.CheckID),
			attribute.String("project", r.Name),
			attribute.String("sha", r.SHA),
			attribute.String("source", r.HeadRef),
			attribute.String("target", r.BaseRef),
			attribute.String("default_branch", r.DefaultBranch),
		),
	)
	defer span.End()

	// If we've gotten here, we can now begin running checks (or trying to)
	cEvent := events.NewCheckEvent(r, ctr)

	err := cEvent.CreateTempDir()
	if err != nil {
		telemetry.SetError(span, err, "Create Temp Dir")
		log.Error().Err(err).Msg("unable to create temp dir")
	}
	defer cEvent.Cleanup(ctx)

	err = vcs.InitializeGitSettings(ctr.Config, ctr.VcsClient)
	if err != nil {
		telemetry.SetError(span, err, "Initialize Git")
		log.Error().Err(err).Msg("unable to initialize git")
		return
	}

	// Clone the repo's BaseRef (main etc) locally into the temp dir we just made
	err = cEvent.CloneRepoLocal(ctx)
	if err != nil {
		// TODO: Cancel event if gitlab etc
		telemetry.SetError(span, err, "Clone Repo Local")
		log.Error().Err(err).Msg("unable to clone repo locally")
		return
	}

	// Merge the most recent changes into the branch we just cloned
	err = cEvent.MergeIntoTarget(ctx)
	if err != nil {
		// TODO: Cancel if gitlab etc
		log.Error().Err(err).Msg("failed to merge into target")
		return
	}

	// Get the diff between the two branches, storing them within the CheckEvent (also returns but discarded here)
	_, err = cEvent.GetListOfChangedFiles(ctx)
	if err != nil {
		// TODO: Cancel if gitlab etc
		log.Error().Err(err).Msg("failed to get list of changed files")
		return
	}

	// Generate a list of affected apps, storing them within the CheckEvent (also returns but discarded here)
	err = cEvent.GenerateListOfAffectedApps(ctx, r.BaseRef)
	if err != nil {
		// TODO: Cancel if gitlab etc
		//mEvent.CancelEvent(ctx, err, "Generate List of Affected Apps")
		log.Error().Err(err).Msg("failed to generate a list of affected apps")
		return
	}

	// At this stage, we've cloned the repo locally, merged the changes into a temp branch, and have calculated
	// what apps/appsets and files have changed. We are now ready to run the Kubechecks suite!
	cEvent.ProcessApps(ctx)
}

// passesLabelFilter checks if the given mergeEvent has a label that starts with "kubechecks:"
// and matches the handler's labelFilter. Returns true if there's a matching label or no
// "kubechecks:" labels are found, and false if a "kubechecks:" label is found but none match
// the labelFilter.
func (h *VCSHookHandler) passesLabelFilter(repo *vcs.Repo) bool {
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
