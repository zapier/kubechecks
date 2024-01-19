package events

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/affected_apps"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/conftest"
	"github.com/zapier/kubechecks/pkg/diff"
	"github.com/zapier/kubechecks/pkg/kubepug"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/pkg/repo_config"
	"github.com/zapier/kubechecks/pkg/validate"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

type CheckEvent struct {
	client         vcs.Client // Client exposing methods to communicate with platform of user choice
	fileList       []string   // What files have changed in this PR/MR
	TempWorkingDir string     // Location of the local repo
	repo           *repo.Repo
	logger         zerolog.Logger
	workerLimits   int
	vcsNote        *pkg.Message

	affectedItems affected_apps.AffectedItems

	cfg *config.ServerConfig

	addedAppsSet map[string]struct{}
	appChannel   chan *v1alpha1.Application
	doneChannel  chan struct{}
}

var inFlight int32

func NewCheckEvent(repo *repo.Repo, cfg *config.ServerConfig) *CheckEvent {
	ce := &CheckEvent{
		cfg:    cfg,
		client: cfg.VcsClient,
		repo:   repo,
	}

	ce.logger = log.Logger.With().Str("repo", repo.Name).Int("event_id", repo.CheckID).Logger()
	return ce
}

// getRepo gets the repo from a CheckEvent. In normal operations a CheckEvent can only be made by the VCSHookHandler
// As the Repo is built from a webhook payload via the VCSClient, it should always be present. If not, error
func (ce *CheckEvent) getRepo(ctx context.Context) (*repo.Repo, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CheckEventGetRepo")
	defer span.End()
	var err error

	if ce.repo == nil {
		ce.logger.Error().Err(err).Msg("Repo is nil, did you forget to create it?")
		return nil, err
	}
	return ce.repo, nil
}

func (ce *CheckEvent) CreateTempDir() error {
	var err error
	ce.TempWorkingDir, err = os.MkdirTemp("/tmp", "kubechecks-mr-clone")
	if err != nil {
		ce.logger.Error().Err(err).Msg("Unable to make temp directory")
		return err
	}
	return nil
}

func (ce *CheckEvent) Cleanup(ctx context.Context) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "Cleanup")
	defer span.End()

	if ce.TempWorkingDir != "" {
		if err := os.RemoveAll(ce.TempWorkingDir); err != nil {
			log.Warn().Err(err).Msgf("failed to remove %s", ce.TempWorkingDir)
		}
	}
}

// CloneRepoLocal takes the repo inside the Check Event and try to clone it locally
func (ce *CheckEvent) CloneRepoLocal(ctx context.Context) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CloneRepoLocal")
	defer span.End()

	return ce.repo.CloneRepoLocal(ctx, ce.TempWorkingDir)
}

// MergeIntoTarget merges the changes from the MR/PR into the base branch
func (ce *CheckEvent) MergeIntoTarget(ctx context.Context) error {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "MergeIntoTarget")
	defer span.End()
	gitRepo, err := ce.getRepo(ctx)
	if err != nil {
		return err
	}

	return gitRepo.MergeIntoTarget(ctx)
}

func (ce *CheckEvent) GetListOfChangedFiles(ctx context.Context) ([]string, error) {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "CheckEventGetListOfChangedFiles")
	defer span.End()

	gitRepo, err := ce.getRepo(ctx)
	if err != nil {
		return nil, err
	}

	if len(ce.fileList) == 0 {
		ce.fileList, err = gitRepo.GetListOfChangedFiles(ctx)
	}

	if err == nil {
		ce.logger.Debug().Msgf("Changed files: %s", strings.Join(ce.fileList, ","))
	}

	return ce.fileList, err
}

// Walks the repo to find any apps or appsets impacted by the changes in the MR/PR.
func (ce *CheckEvent) GenerateListOfAffectedApps(ctx context.Context, targetBranch string) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "GenerateListOfAffectedApps")
	defer span.End()
	var err error

	var matcher affected_apps.Matcher
	cfg, _ := repo_config.LoadRepoConfig(ce.TempWorkingDir)
	if cfg != nil {
		log.Debug().Msg("using the config matcher")
		matcher = affected_apps.NewConfigMatcher(cfg)
	} else {
		log.Debug().Msg("using an argocd matcher")
		matcher, err = affected_apps.NewArgocdMatcher(ce.cfg.VcsToArgoMap, ce.repo, ce.TempWorkingDir)
		if err != nil {
			return errors.Wrap(err, "failed to create argocd matcher")
		}
	}
	ce.affectedItems, err = matcher.AffectedApps(ctx, ce.fileList, targetBranch)
	if err != nil {
		telemetry.SetError(span, err, "Get Affected Apps")
		ce.logger.Error().Err(err).Msg("could not get list of affected apps and appsets")
	}
	span.SetAttributes(
		attribute.Int("numAffectedApps", len(ce.affectedItems.Applications)),
		attribute.Int("numAffectedAppSets", len(ce.affectedItems.ApplicationSets)),
		attribute.String("affectedApps", fmt.Sprintf("%+v", ce.affectedItems.Applications)),
		attribute.String("affectedAppSets", fmt.Sprintf("%+v", ce.affectedItems.ApplicationSets)),
	)
	ce.logger.Debug().Msgf("Affected apps: %+v", ce.affectedItems.Applications)
	ce.logger.Debug().Msgf("Affected appSets: %+v", ce.affectedItems.ApplicationSets)

	return err
}

func (ce *CheckEvent) ProcessApps(ctx context.Context) {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "ProcessApps",
		trace.WithAttributes(
			attribute.String("affectedApps", fmt.Sprintf("%+v", ce.affectedItems.Applications)),
			attribute.Int("workerLimits", ce.workerLimits),
			attribute.Int("numAffectedApps", len(ce.affectedItems.Applications)),
		))
	defer span.End()

	err := ce.client.TidyOutdatedComments(ctx, ce.repo)
	if err != nil {
		ce.logger.Error().Err(err).Msg("Failed to tidy outdated comments")
	}

	if len(ce.affectedItems.Applications) <= 0 && len(ce.affectedItems.ApplicationSets) <= 0 {
		ce.logger.Info().Msg("No affected apps or appsets, skipping")
		ce.client.PostMessage(ctx, ce.repo, ce.repo.CheckID, "No changes")
		return
	}

	// Concurrently process all apps, with a corresponding error channel for reporting back failures
	ce.addedAppsSet = make(map[string]struct{})
	ce.appChannel = make(chan *v1alpha1.Application, len(ce.affectedItems.Applications)*2)
	ce.doneChannel = make(chan struct{}, len(ce.affectedItems.Applications)*2)

	// If the number of affected apps that we have is less than our worker limit, lower the worker limit
	if ce.workerLimits > len(ce.affectedItems.Applications) {
		ce.workerLimits = len(ce.affectedItems.Applications)
	}

	// We make one comment per run, containing output for all the apps
	ce.vcsNote = ce.createNote(ctx)

	for w := 0; w <= ce.workerLimits; w++ {
		go ce.appWorkers(ctx, w)
	}

	// Produce apps onto channel
	for _, app := range ce.affectedItems.Applications {
		ce.queueApp(app)
	}

	returnCount := 0
	for range ce.doneChannel {
		ce.logger.Debug().Msg("finished an app")

		returnCount++
		ce.logger.Debug().Int("done apps", returnCount).Int("all apps", len(ce.addedAppsSet)).Msg("completed apps")

		if returnCount == len(ce.addedAppsSet) {
			ce.logger.Debug().Msg("Closing channels")
			close(ce.appChannel)
			close(ce.doneChannel)
		}
	}
	ce.logger.Info().Msg("Finished")

	comment := ce.vcsNote.BuildComment(ctx)
	if err = ce.client.UpdateMessage(ctx, ce.vcsNote, comment); err != nil {
		ce.logger.Error().Err(err).Msg("failed to push comment")
	}

	worstStatus := ce.vcsNote.WorstState()
	ce.CommitStatus(ctx, worstStatus)
}

func (ce *CheckEvent) queueApp(app v1alpha1.Application) {
	name := app.Name
	dir := app.Spec.GetSource().Path

	if _, ok := ce.addedAppsSet[name]; ok {
		return
	}

	ce.addedAppsSet[name] = struct{}{}

	logger := ce.logger.Debug().
		Str("app", name).
		Str("dir", dir).
		Str("cluster-name", app.Spec.Destination.Name).
		Str("cluster-server", app.Spec.Destination.Server)

	logger.Msg("producing app on channel")
	ce.appChannel <- &app
	logger.Msg("finished producing app")
}

// CommitStatus sets the commit status on the MR
// To set the PR/MR status
func (ce *CheckEvent) CommitStatus(ctx context.Context, status pkg.CommitState) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CommitStatus")
	defer span.End()

	if err := ce.client.CommitStatus(ctx, ce.repo, status); err != nil {
		log.Warn().Err(err).Msg("failed to update commit status")
	}
}

// Process all apps on the provided channel
func (ce *CheckEvent) appWorkers(ctx context.Context, workerID int) {
	for app := range ce.appChannel {
		if app != nil {
			ce.logger.Info().Int("workerID", workerID).Str("app", app.Name).Msg("Processing App")
			ce.processApp(ctx, *app)
		} else {
			log.Warn().Msg("appWorkers received a nil app")
		}

		ce.doneChannel <- struct{}{}
	}
}

// processApp is a function that validates and processes a given application manifest against various checks,
// such as ArgoCD schema validation, diff generation, conftest policy validation, and pre-upgrade checks using kubepug.
// It takes a context (ctx), application name (app), directory (dir) as input and returns an error if any check fails.
// The processing is performed concurrently using Go routines and error groups. Any check results are sent through
// the returnChan. The function also manages the inFlight atomic counter to track active processing routines.
func (ce *CheckEvent) processApp(ctx context.Context, app v1alpha1.Application) {
	appName := app.Name
	dir := app.Spec.GetSource().Path

	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "processApp", trace.WithAttributes(
		attribute.String("app", appName),
		attribute.String("dir", dir),
	))
	defer span.End()

	atomic.AddInt32(&inFlight, 1)
	defer atomic.AddInt32(&inFlight, -1)

	start := time.Now()
	ce.logger.Info().Str("app", appName).Msg("Adding new app")
	// Build a new section for this app in the parent comment
	ce.vcsNote.AddNewApp(ctx, appName)

	ce.logger.Debug().Msgf("Getting manifests for app: %s with code at %s/%s", appName, ce.TempWorkingDir, dir)
	manifests, err := argo_client.GetManifestsLocal(ctx, appName, ce.TempWorkingDir, dir, app)
	if err != nil {
		ce.logger.Error().Err(err).Msgf("Unable to get manifests for %s in %s", appName, dir)
		cr := pkg.CheckResult{State: pkg.StateError, Summary: "Unable to get manifests", Details: fmt.Sprintf("```\n%s\n```", ce.cleanupGetManifestsError(err))}
		ce.vcsNote.AddToAppMessage(ctx, appName, cr)
		return
	}

	// Argo diff logic wants unformatted manifests but everything else wants them as YAML, so we prepare both
	formattedManifests := argo_client.FormatManifestsYAML(manifests)
	ce.logger.Trace().Msgf("Manifests:\n%+v\n", formattedManifests)

	k8sVersion, err := argo_client.GetArgoClient().GetKubernetesVersionByApplication(ctx, app)
	if err != nil {
		ce.logger.Error().Err(err).Msg("Error retrieving the Kubernetes version")
		k8sVersion = viper.GetString("fallback-k8s-version")
	} else {
		k8sVersion = fmt.Sprintf("%s.0", k8sVersion)
		ce.logger.Info().Msgf("Kubernetes version: %s", k8sVersion)
	}

	var wg sync.WaitGroup

	run := ce.createRunner(span, ctx, appName, &wg)

	run("validating app against schema", ce.validateSchemas(ctx, appName, k8sVersion, ce.TempWorkingDir, formattedManifests))
	run("generating diff for app", ce.generateDiff(ctx, app, manifests, ce.queueApp))

	if viper.GetBool("enable-conftest") {
		run("validation policy", ce.validatePolicy(ctx, appName))
	}

	run("running pre-upgrade check", ce.runPreupgradeCheck(ctx, appName, k8sVersion, formattedManifests))

	wg.Wait()

	ce.vcsNote.SetFooter(start, ce.repo.SHA)
}

type checkFunction func() (pkg.CheckResult, error)

const (
	errorCommentFormat = `
:warning:  **Error while %s** :warning: 
` + "```" + `
%v
` + "```" + `

Check kubechecks application logs for more information.
`
)

func (ce *CheckEvent) createRunner(span trace.Span, grpCtx context.Context, app string, wg *sync.WaitGroup) func(string, checkFunction) {
	return func(desc string, fn checkFunction) {
		wg.Add(1)

		go func() {
			defer func() {
				wg.Done()

				if r := recover(); r != nil {
					ce.logger.Error().Str("app", app).Str("check", desc).Msgf("panic while running check")

					telemetry.SetError(span, fmt.Errorf("%v", r), desc)
					result := pkg.CheckResult{State: pkg.StatePanic, Summary: desc, Details: fmt.Sprintf(errorCommentFormat, desc, r)}
					ce.vcsNote.AddToAppMessage(grpCtx, app, result)
				}
			}()

			ce.logger.Info().Str("app", app).Str("check", desc).Msgf("running check")
			cr, err := fn()
			ce.logger.Info().
				Err(err).
				Str("app", app).
				Str("check", desc).
				Uint8("result", uint8(cr.State)).
				Msg("check result")

			if err != nil {
				telemetry.SetError(span, err, desc)
				result := pkg.CheckResult{State: pkg.StateError, Summary: desc, Details: fmt.Sprintf(errorCommentFormat, desc, err)}
				ce.vcsNote.AddToAppMessage(grpCtx, app, result)
				return
			}

			ce.vcsNote.AddToAppMessage(grpCtx, app, cr)

			ce.logger.Info().Str("app", app).Str("check", desc).Str("result", cr.State.BareString()).Msgf("check done")
		}()
	}
}

func (ce *CheckEvent) runPreupgradeCheck(grpCtx context.Context, app string, k8sVersion string, formattedManifests []string) func() (pkg.CheckResult, error) {
	return func() (pkg.CheckResult, error) {
		s, err := kubepug.CheckApp(grpCtx, app, k8sVersion, formattedManifests)
		if err != nil {
			return pkg.CheckResult{}, err
		}

		return s, nil
	}
}

func (ce *CheckEvent) validatePolicy(ctx context.Context, app string) func() (pkg.CheckResult, error) {
	return func() (pkg.CheckResult, error) {
		argoApp, err := argo_client.GetArgoClient().GetApplicationByName(ctx, app)
		if err != nil {
			return pkg.CheckResult{}, errors.Wrapf(err, "could not retrieve ArgoCD App data: %q", app)
		}

		cr, err := conftest.Conftest(ctx, argoApp, ce.TempWorkingDir, ce.client)
		if err != nil {
			return pkg.CheckResult{}, err
		}

		return cr, nil
	}
}

func (ce *CheckEvent) generateDiff(ctx context.Context, app v1alpha1.Application, manifests []string, addApp func(app v1alpha1.Application)) func() (pkg.CheckResult, error) {
	return func() (pkg.CheckResult, error) {
		cr, rawDiff, err := diff.GetDiff(ctx, manifests, app, addApp)
		if err != nil {
			return pkg.CheckResult{}, err
		}

		diff.AIDiffSummary(ctx, ce.vcsNote, app.Name, manifests, rawDiff)

		return cr, nil
	}
}

func (ce *CheckEvent) validateSchemas(ctx context.Context, app, k8sVersion, tempRepoPath string, formattedManifests []string) func() (pkg.CheckResult, error) {
	return func() (pkg.CheckResult, error) {
		cr, err := validate.ArgoCdAppValidate(ctx, app, k8sVersion, tempRepoPath, formattedManifests)
		if err != nil {
			return pkg.CheckResult{}, err
		}

		return cr, nil
	}
}

// Creates a generic Note struct that we can write into across all worker threads
func (ce *CheckEvent) createNote(ctx context.Context) *pkg.Message {
	ctx, span := otel.Tracer("check").Start(ctx, "createNote")
	defer span.End()

	ce.logger.Info().Msgf("Creating note")

	return ce.client.PostMessage(ctx, ce.repo, ce.repo.CheckID, ":hourglass: kubechecks running ... ")
}

// cleanupGetManifestsError takes an error as input and returns a simplified and more user-friendly error message.
// It reformats Helm error messages by removing excess information, and makes file paths relative to the git repo root.
func (ce *CheckEvent) cleanupGetManifestsError(err error) string {
	// cleanup the chonky helm error message for a better DX
	errStr := err.Error()
	if strings.Contains(errStr, "helm template") && strings.Contains(errStr, "failed exit status") {
		errMsgIdx := strings.Index(errStr, "Error:")
		errStr = fmt.Sprintf("Helm %s", errStr[errMsgIdx:])
	}

	// strip the temp directory from any files mentioned to make file paths relative to git repo root
	errStr = strings.ReplaceAll(errStr, ce.TempWorkingDir+"/", "")

	return errStr
}
