package events

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/affected_apps"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/conftest"
	"github.com/zapier/kubechecks/pkg/diff"
	"github.com/zapier/kubechecks/pkg/kubepug"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/pkg/repo_config"
	"github.com/zapier/kubechecks/pkg/validate"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
	"github.com/zapier/kubechecks/telemetry"
)

type CheckEvent struct {
	client         vcs_clients.Client // Client exposing methods to communicate with platform of user choice
	fileList       []string           // What files have changed in this PR/MR
	repoFiles      []string           // All files in this repository
	TempWorkingDir string             // Location of the local repo
	repo           *repo.Repo
	logger         zerolog.Logger
	workerLimits   int
	vcsNote        *vcs_clients.Message

	affectedItems affected_apps.AffectedItems

	cfg *pkg.ServerConfig
}

const (
	errorCommentFormat = `
:warning:  **Error while %s** :warning: 
` + "```" + `
%v
` + "```" + `

Check kubechecks application logs for more information.
`
)

var (
	hostname = ""
	inFlight int32
)

func init() {
	hostname, _ = os.Hostname()
}

func NewCheckEvent(repo *repo.Repo, client vcs_clients.Client, cfg *pkg.ServerConfig) *CheckEvent {
	ce := &CheckEvent{
		cfg:    cfg,
		client: client,
		repo:   repo,
	}

	ce.logger = log.Logger.With().Str("repo", repo.Name).Int("event_id", repo.CheckID).Logger()
	return ce
}

// Get the Repo from a CheckEvent. In normal operations a CheckEvent can only be made by the VCSHookHandler
// As the Repo is built from a webhook payload via the VCSClient, it should always be present. If not, error
func (ce *CheckEvent) GetRepo(ctx context.Context) (*repo.Repo, error) {
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

// InitializeGit sets the username and email for a git repo
func (ce *CheckEvent) InitializeGit(ctx context.Context) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "InitializeGit")
	defer span.End()

	return repo.InitializeGitSettings(ce.repo.Username, ce.repo.Email)
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
	gitRepo, err := ce.GetRepo(ctx)
	if err != nil {
		return err
	}

	return gitRepo.MergeIntoTarget(ctx)
}

func (ce *CheckEvent) GetListOfChangedFiles(ctx context.Context) ([]string, error) {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "CheckEventGetListOfChangedFiles")
	defer span.End()

	gitRepo, err := ce.GetRepo(ctx)
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
func (ce *CheckEvent) GenerateListOfAffectedApps(ctx context.Context) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "GenerateListOfAffectedApps")
	defer span.End()
	var err error

	var matcher affected_apps.Matcher
	cfg, _ := repo_config.LoadRepoConfig(ce.TempWorkingDir)
	if cfg != nil {
		log.Debug().Msg("using the config matcher")
		matcher = affected_apps.NewConfigMatcher(cfg)
	} else if viper.GetBool("monitor-all-applications") {
		log.Debug().Msg("using an argocd matcher")
		matcher = affected_apps.NewArgocdMatcher(ce.cfg.VcsToArgoMap, ce.repo)
		if err != nil {
			return errors.Wrap(err, "failed to create argocd matcher")
		}
	} else {
		log.Debug().Msg("using best effort matcher")
		ce.repoFiles, err = ce.repo.GetListOfRepoFiles()
		if err != nil {
			telemetry.SetError(span, err, "Get List of Repo Files")

			ce.logger.Error().Err(err).Msg("could not get list of repo files")
			// continue with an empty list
			ce.repoFiles = []string{}
		}
		matcher = affected_apps.NewBestEffortMatcher(ce.repo.Name, ce.repoFiles)
	}
	ce.affectedItems, err = matcher.AffectedApps(ctx, ce.fileList)
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

type appStruct struct {
	name string
	dir  string
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
	appChannel := make(chan appStruct, len(ce.affectedItems.Applications))
	errChannel := make(chan error, len(ce.affectedItems.Applications))

	// If the number of affected apps that we have is less than our worker limit, lower the worker limit
	if ce.workerLimits > len(ce.affectedItems.Applications) {
		ce.workerLimits = len(ce.affectedItems.Applications)
	}

	// We make one comment per run, containing output for all the apps
	ce.vcsNote = ce.createNote(ctx)

	for w := 0; w <= ce.workerLimits; w++ {
		go ce.appWorkers(ctx, w, appChannel, errChannel)
	}

	// Produce apps onto channel
	for _, app := range ce.affectedItems.Applications {
		a := appStruct{
			name: app.Name,
			dir:  app.Path,
		}
		ce.logger.Trace().Str("app", a.name).Str("dir", a.dir).Msg("producing app on channel")
		appChannel <- a
	}

	returnCount := 0
	resultError := false
	for err := range errChannel {
		returnCount++
		if err != nil {
			resultError = true
			ce.logger.Error().Err(err).Msg("error running tool")
		}
		if returnCount == len(ce.affectedItems.Applications) {
			ce.logger.Debug().Msg("Closing channels")
			close(appChannel)
			close(errChannel)
		}
	}
	ce.logger.Info().Msg("Finished")

	if resultError {
		ce.CommitStatus(ctx, vcs_clients.Failure)
		ce.logger.Error().Msg("Errors found")
		return
	}

	ce.CommitStatus(ctx, vcs_clients.Success)
}

// CommitStatus takes one of "success", "failure", "pending" or "error" and pass off to client
// To set the PR/MR status
func (ce *CheckEvent) CommitStatus(ctx context.Context, status vcs_clients.CommitState) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CommitStatus")
	defer span.End()

	if err := ce.client.CommitStatus(ctx, ce.repo, status); err != nil {
		log.Warn().Err(err).Msg("failed to update commit status")
	}
}

// Process all apps on the provided channel
func (ce *CheckEvent) appWorkers(ctx context.Context, workerID int, appChannel chan appStruct, resultChannel chan error) {
	for app := range appChannel {
		ce.logger.Info().Int("workerID", workerID).Str("app", app.name).Msg("Processing App")
		resultChannel <- ce.processApp(ctx, app.name, app.dir)
	}
}

// processApp is a function that validates and processes a given application manifest against various checks,
// such as ArgoCD schema validation, diff generation, conftest policy validation, and pre-upgrade checks using kubepug.
// It takes a context (ctx), application name (app), directory (dir) as input and returns an error if any check fails.
// The processing is performed concurrently using Go routines and error groups. Any check results are sent through
// the returnChan. The function also manages the inFlight atomic counter to track active processing routines.
func (ce *CheckEvent) processApp(ctx context.Context, app, dir string) error {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "processApp", trace.WithAttributes(
		attribute.String("app", app),
		attribute.String("dir", dir),
	))
	defer span.End()

	atomic.AddInt32(&inFlight, 1)
	defer atomic.AddInt32(&inFlight, -1)

	start := time.Now()
	ce.logger.Info().Str("app", app).Msg("Adding new app")
	// Build a new section for this app in the parent comment
	ce.vcsNote.AddNewApp(ctx, app)

	ce.logger.Debug().Msgf("Getting manifests for app: %s with code at %s/%s", app, ce.TempWorkingDir, dir)
	manifests, err := argo_client.GetManifestsLocal(ctx, app, ce.TempWorkingDir, dir)
	if err != nil {
		ce.logger.Error().Err(err).Msgf("Unable to get manifests for %s in %s", app, dir)
		ce.vcsNote.AddToAppMessage(ctx, app, fmt.Sprintf("Unable to get manifests for application: \n\n ```\n%s\n```", ce.cleanupGetManifestsError(err)))
		return nil
	}

	// Argo diff logic wants unformatted manifests but everything else wants them as YAML, so we prepare both
	formattedManifests := argo_client.FormatManifestsYAML(manifests)
	ce.logger.Trace().Msgf("Manifests:\n%+v\n", formattedManifests)

	k8sVersion, err := argo_client.GetArgoClient().GetKubernetesVersionByApplicationName(ctx, app)
	if err != nil {
		ce.logger.Error().Err(err).Msg("Error retrieving the Kubernetes version")
		k8sVersion = viper.GetString("fallback-k8s-version")
	} else {
		k8sVersion = fmt.Sprintf("%s.0", k8sVersion)
		ce.logger.Info().Msgf("Kubernetes version: %s", k8sVersion)
	}

	grp, grpCtx := errgroup.WithContext(ctx)
	wrap := ce.createWrapper(span, grpCtx, app)

	grp.Go(wrap("validating app against schema", ce.validateSchemas(grpCtx, app, k8sVersion, formattedManifests)))
	grp.Go(wrap("generating diff for app", ce.generateDiff(grpCtx, app, manifests)))

	if viper.GetBool("enable-conftest") {
		grp.Go(wrap("validation policy", ce.validatePolicy(grpCtx, app)))
	}

	grp.Go(wrap("running pre-upgrade check", ce.runPreupgradeCheck(grpCtx, app, k8sVersion, formattedManifests)))

	err = grp.Wait()
	if err != nil {
		telemetry.SetError(span, err, "running checks")
	}

	ce.vcsNote.AddToAppMessage(ctx, app, renderInfoFooter(time.Since(start), ce.repo.SHA))

	return err
}

type checkFunction func() (string, error)

func (ce *CheckEvent) createWrapper(span trace.Span, grpCtx context.Context, app string) func(string, checkFunction) func() error {
	return func(desc string, fn checkFunction) func() error {
		return func() error {
			defer func() {
				if r := recover(); r != nil {
					telemetry.SetError(span, fmt.Errorf("%v", r), desc)
					ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, desc, r))
				}
			}()

			s, err := fn()
			if err != nil {
				telemetry.SetError(span, err, desc)
				ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, desc, err))
				return errors.Wrapf(err, "error while %s", desc)
			}

			if s != "" {
				ce.vcsNote.AddToAppMessage(grpCtx, app, s)
			}

			return nil
		}
	}
}

func (ce *CheckEvent) runPreupgradeCheck(grpCtx context.Context, app string, k8sVersion string, formattedManifests []string) func() (string, error) {
	return func() (string, error) {
		s, err := kubepug.CheckApp(grpCtx, app, k8sVersion, formattedManifests)
		if err != nil {
			return "", err
		}

		return s, nil
	}
}

func (ce *CheckEvent) validatePolicy(ctx context.Context, app string) func() (string, error) {
	return func() (string, error) {
		argoApp, err := argo_client.GetArgoClient().GetApplicationByName(ctx, app)
		if err != nil {
			return "", errors.Wrapf(err, "could not retrieve ArgoCD App data: %q", app)
		}

		s, err := conftest.Conftest(ctx, argoApp, ce.TempWorkingDir)
		if err != nil {
			return "", err
		}

		return s, nil
	}
}

func (ce *CheckEvent) generateDiff(ctx context.Context, app string, manifests []string) func() (string, error) {
	return func() (string, error) {
		s, rawDiff, err := diff.GetDiff(ctx, app, manifests)
		if err != nil {
			return "", err
		}

		diff.AIDiffSummary(ctx, ce.vcsNote, app, manifests, rawDiff)

		return s, nil
	}
}

func (ce *CheckEvent) validateSchemas(ctx context.Context, app string, k8sVersion string, formattedManifests []string) func() (string, error) {
	return func() (string, error) {
		s, err := validate.ArgoCdAppValidate(ctx, app, k8sVersion, formattedManifests)
		if err != nil {
			return "", err
		}

		return s, nil
	}
}

// Creates a generic Note struct that we can write into across all worker threads
func (ce *CheckEvent) createNote(ctx context.Context) *vcs_clients.Message {
	ctx, span := otel.Tracer("check").Start(ctx, "createNote")
	defer span.End()

	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "# Kubechecks Report:\n")
	ce.logger.Info().Msgf("Creating note")

	return ce.client.PostMessage(ctx, ce.repo, ce.repo.CheckID, sb.String())
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

func renderInfoFooter(duration time.Duration, commitSHA string) string {
	if viper.GetBool("show-debug-info") {
		label := viper.GetString("label-filter")
		envStr := ""
		if label != "" {
			envStr = fmt.Sprintf(", Env: %s", label)
		}
		return fmt.Sprintf("<small>_Done: Pod: %s, Dur: %v, SHA: %s%s_<small>\n", hostname, duration, pkg.GitCommit, envStr)
	} else {
		return fmt.Sprintf("<small>_Done. CommitSHA: %s_<small>\n", commitSHA)
	}
}
