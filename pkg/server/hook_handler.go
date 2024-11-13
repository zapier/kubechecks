package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/events"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/vcs"
)

var tracer = otel.Tracer("pkg/server")

type VCSHookHandler struct {
	ctr        container.Container
	processors []checks.ProcessorEntry
}

func NewVCSHookHandler(ctr container.Container, processors []checks.ProcessorEntry) *VCSHookHandler {
	return &VCSHookHandler{
		ctr:        ctr,
		processors: processors,
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
		if errors.Is(err, vcs.ErrInvalidType) {
			log.Debug().Msg("Ignoring event, not a supported request")
			return c.String(http.StatusOK, "Skipped")
		}

		// TODO: do something ELSE with the error
		log.Error().Err(err).Msg("Failed to create a repository locally")
		return echo.ErrBadRequest
	}

	// We now have a generic repo with all the info we need to start processing an event. Hand off to the event processor
	go h.processCheckEvent(ctx, pr)
	return c.String(http.StatusAccepted, "Accepted")
}

// Takes a constructed Repo, and attempts to run the Kubechecks processing suite against it.
// If the Repo is not yet populated, this will fail.
func (h *VCSHookHandler) processCheckEvent(ctx context.Context, pullRequest vcs.PullRequest) {
	if !h.passesLabelFilter(pullRequest) {
		log.Warn().Str("label-filter", h.ctr.Config.LabelFilter).Msg("ignoring event, did not have matching label")
		return
	}

	ProcessCheckEvent(ctx, pullRequest, h.ctr, h.processors)
}

type RepoDirectory struct{}

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

	// repo cache
	repoMgr := git.NewRepoManager(ctr.Config)
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
