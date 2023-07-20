package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/events"
	"github.com/zapier/kubechecks/pkg/github_client"
	"github.com/zapier/kubechecks/pkg/gitlab_client"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
	"github.com/zapier/kubechecks/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type VCSHookHandler struct {
	client    vcs_clients.Client
	tokenUser string
	cfg       *pkg.ServerConfig
	// labelFilter is a string specifying the required label name to filter merge events by; if empty, all merge events will pass the filter.
	labelFilter string
}

var once sync.Once
var vcsClient vcs_clients.Client // Currently, only allow one client at a time
var tokenUser string
var ProjectHookPath = "/gitlab/project"

// High level type representing the fields we care about from an arbitrary Git repository
func GetVCSClient() (vcs_clients.Client, string) {
	once.Do(func() {
		vcsClient, tokenUser = createVCSClient()
	})
	return vcsClient, tokenUser
}

func createVCSClient() (vcs_clients.Client, string) {
	// Determine what client to use based on set config (default Gitlab)
	clientType := viper.GetString("vcs-type")
	// All hooks set up follow the convention /VCS_PROVIDER/project
	ProjectHookPath = fmt.Sprintf("/%s/project", clientType)
	switch clientType {
	case "gitlab":
		return gitlab_client.GetGitlabClient()
	case "github":
		return github_client.GetGithubClient()
	default:
		log.Fatal().Msgf("Unknown VCS type: %s", clientType)
		return nil, ""
	}

}

func NewVCSHookHandler(cfg *pkg.ServerConfig) *VCSHookHandler {
	client, tokenUser := GetVCSClient()
	labelFilter := viper.GetString("label-filter")

	return &VCSHookHandler{
		client:      client,
		tokenUser:   tokenUser,
		cfg:         cfg,
		labelFilter: labelFilter,
	}
}
func (h *VCSHookHandler) AttachHandlers(grp *echo.Group) {
	log.Info().Str("path", GetServer().hooksPrefix()).Msg("setting up hook handler")
	grp.POST(ProjectHookPath, h.groupHandler)
	log.Info().Str("path", GetServer().hooksPrefix()).Str("projectPath", ProjectHookPath).Msg("hook handler setup complete")
}

func (h *VCSHookHandler) groupHandler(c echo.Context) error {
	ctx := context.Background()
	log.Debug().Msg("Received hook request")
	// Always verify, even if no secret (no op if no secret)
	payload, err := h.client.VerifyHook(c.Request(), h.cfg.WebhookSecret)
	if err != nil {
		log.Err(err).Msg("Failed to verify hook")
		return c.String(http.StatusUnauthorized, "Unauthorized")
	}

	eventRequest, err := h.client.ParseHook(c.Request(), payload)
	if err != nil {
		// TODO: do something w/ error
		log.Error().Err(err).Msg("Failed to parse hook payload. Are you using the right client?")
		return echo.ErrBadRequest
	}

	// At this point, our client has validated the secret, and parsed a valid event.
	// We try to build a generic Repo from this data, to construct our CheckEvent
	repo, err := h.client.CreateRepo(ctx, eventRequest)
	if err != nil {
		switch err {
		case vcs_clients.ErrInvalidType:
			log.Debug().Msg("Ignoring event, not a merge request")
			return c.String(http.StatusOK, "Skipped")
		default:
			// TODO: do something ELSE with the error
			log.Error().Err(err).Msg("Failed to create a repository locally")
			return echo.ErrBadRequest
		}
	}

	// We now have a generic repo with all the info we need to start processing an event. Hand off to the event processor
	go h.processCheckEvent(ctx, repo)
	return c.String(http.StatusAccepted, "Accepted")
}

// Takes a constructed Repo, and attempts to run the Kubechecks processing suite against it.
// If the Repo is not yet populated, this will fail.
func (h *VCSHookHandler) processCheckEvent(ctx context.Context, repo *repo.Repo) {
	var span trace.Span
	ctx, span = otel.Tracer("Kubechecks").Start(ctx, "processCheckEvent",
		trace.WithAttributes(
			attribute.Int("mr_id", repo.CheckID),
			attribute.String("project", repo.Name),
			attribute.String("sha", repo.SHA),
			attribute.String("source", repo.HeadRef),
			attribute.String("target", repo.BaseRef),
			attribute.String("default_branch", repo.DefaultBranch),
		),
	)
	defer span.End()

	if !h.passesLabelFilter(repo) {
		log.Warn().Str("label-filter", h.labelFilter).Msg("ignoring event, did not have matching label")
		return
	}

	// If we've gotten here, we can now begin running checks (or trying to)
	cEvent := events.NewCheckEvent(repo, h.client, h.cfg)

	err := cEvent.CreateTempDir()
	if err != nil {
		telemetry.SetError(span, err, "Create Temp Dir")
		log.Error().Err(err).Msg("unable to create temp dir")
	}
	defer cEvent.Cleanup(ctx)

	err = cEvent.InitializeGit(ctx)
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
		return
	}

	// Get the diff between the two branches, storing them within the CheckEvent (also returns but discarded here)
	_, err = cEvent.GetListOfChangedFiles(ctx)
	if err != nil {
		// TODO: Cancel if gitlab etc
		return
	}

	// Generate a list of affected apps, storing them within the CheckEvent (also returns but discarded here)
	err = cEvent.GenerateListOfAffectedApps(ctx)
	if err != nil {
		// TODO: Cancel if gitlab etc
		//mEvent.CancelEvent(ctx, err, "Generate List of Affected Apps")
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
func (h *VCSHookHandler) passesLabelFilter(repo *repo.Repo) bool {
	foundKubechecksLabel := false

	for _, label := range repo.Labels {
		log.Debug().Str("check_label", label).Msg("checking label for match")
		// Check if label starts with "kubechecks:"
		if strings.HasPrefix(label, "kubechecks:") {
			foundKubechecksLabel = true

			// Get the remaining string after "kubechecks:"
			remainingString := strings.TrimPrefix(label, "kubechecks:")
			if remainingString == h.labelFilter {
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
	if h.labelFilter != "" {
		return false
	}

	return true
}
