package events

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

type CheckEvent struct {
	client          vcs_clients.Client // Client exposing methods to communicate with platform of user choice
	fileList        []string           // What files have changed in this PR/MR
	repoFiles       []string           // All files in this repository
	TempWorkingDir  string             // Location of the local repo
	repo            *repo.Repo
	logger          zerolog.Logger
	affectedApps    map[string]string
	workerLimits    int
	affectedAppSets []string
	vcsNote         *vcs_clients.Message
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

func NewCheckEvent(repo *repo.Repo, client vcs_clients.Client) *CheckEvent {
	ce := &CheckEvent{
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
		os.RemoveAll(ce.TempWorkingDir)
	}
}

// Ensure we init git for this Check Event
func (ce *CheckEvent) InitializeGit(ctx context.Context) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "InitializeGit")
	defer span.End()

	return repo.InitializeGitSettings(ce.repo.Username, ce.repo.Email)
}

// Take the repo inside the Check Event and try to clone it locally
func (ce *CheckEvent) CloneRepoLocal(ctx context.Context) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CloneRepoLocal")
	defer span.End()

	return ce.repo.CloneRepoLocal(ctx, ce.TempWorkingDir)
}

// Merge the changes from the MR/PR into the base branch
func (ce *CheckEvent) MergeIntoTarget(ctx context.Context) error {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "MergeIntoTarget")
	defer span.End()
	repo, err := ce.GetRepo(ctx)
	if err != nil {
		return err
	}

	return repo.MergeIntoTarget(ctx)
}

func (ce *CheckEvent) GetListOfChangedFiles(ctx context.Context) ([]string, error) {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "CheckEventGetListOfChangedFiles")
	defer span.End()

	repo, err := ce.GetRepo(ctx)
	if err != nil {
		return nil, err
	}

	if len(ce.fileList) == 0 {
		ce.fileList, err = repo.GetListOfChangedFiles(ctx)
	}

	if err == nil {
		ce.logger.Debug().Msgf("Changed files: %s", strings.Join(ce.fileList, ","))
	}

	return ce.fileList, err
}

// Walks the repo to find any apps or appsets impacted by the changes in the MR/PR.
func (ce *CheckEvent) GenerateListOfAffectedApps(ctx context.Context) (map[string]string, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "GenerateListOfAffectedApps")
	defer span.End()
	var err error

	var matcher affected_apps.Matcher
	cfg, _ := repo_config.LoadRepoConfig(ce.TempWorkingDir)
	if cfg != nil {
		matcher = affected_apps.NewConfigMatcher(cfg)
	} else {
		ce.repoFiles, err = ce.repo.GetListOfRepoFiles()
		if err != nil {
			telemetry.SetError(span, err, "Get List of Repo Files")

			ce.logger.Error().Err(err).Msg("could not get list of repo files")
			// continue with an empty list
			ce.repoFiles = []string{}
		}
		matcher = affected_apps.NewBestEffortMatcher(ce.repo.Name, ce.repoFiles)
	}
	ce.affectedApps, ce.affectedAppSets, err = matcher.AffectedApps(ctx, ce.fileList)
	if err != nil {
		telemetry.SetError(span, err, "Get Affected Apps")
		ce.logger.Error().Err(err).Msg("could not get list of affected apps and appsets")
	}
	span.SetAttributes(
		attribute.Int("numAffectedApps", len(ce.affectedApps)),
		attribute.Int("numAffectedAppSets", len(ce.affectedAppSets)),
		attribute.String("affectedApps", fmt.Sprintf("%+v", ce.affectedApps)),
		attribute.String("affectedAppSets", fmt.Sprintf("%+v", ce.affectedAppSets)),
	)
	ce.logger.Debug().Msgf("Affected apps: %+v", ce.affectedApps)
	ce.logger.Debug().Msgf("Affected appSets: %+v", ce.affectedAppSets)

	return ce.affectedApps, err
}

type appStruct struct {
	name string
	dir  string
}

func (ce *CheckEvent) ProcessApps(ctx context.Context) {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "ProcessApps",
		trace.WithAttributes(
			attribute.String("affectedApps", fmt.Sprintf("%+v", ce.affectedApps)),
			attribute.Int("workerLimits", ce.workerLimits),
			attribute.Int("numAffectedApps", len(ce.affectedApps)),
		))
	defer span.End()

	err := ce.client.TidyOutdatedComments(ctx, ce.repo)
	if err != nil {
		ce.logger.Error().Err(err).Msg("Failed to tidy outdated comments")
	}

	if len(ce.affectedApps) <= 0 && len(ce.affectedAppSets) <= 0 {
		ce.logger.Info().Msg("No affected apps or appsets, skipping")
		ce.client.PostMessage(ctx, ce.repo.FullName, ce.repo.CheckID, "No changes")
		return
	}

	// Concurrently process all apps, with a corresponding error channel for reporting back failures
	appChannel := make(chan appStruct, len(ce.affectedApps))
	errChannel := make(chan error, len(ce.affectedApps))

	// If the number of affected apps that we have is less than our worker limit, lower the worker limit
	if ce.workerLimits > len(ce.affectedApps) {
		ce.workerLimits = len(ce.affectedApps)
	}

	// We make one comment per run, containing output for all the apps
	ce.vcsNote = ce.createNote(ctx)

	for w := 0; w <= ce.workerLimits; w++ {
		go ce.appWorkers(ctx, w, appChannel, errChannel)
	}

	// Produce apps onto channel
	for app, dir := range ce.affectedApps {
		a := appStruct{
			name: app,
			dir:  dir,
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
		if returnCount == len(ce.affectedApps) {
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

// Take one of "success", "failure", "pending" or "error" and pass off to client
// To set the PR/MR status
func (ce *CheckEvent) CommitStatus(ctx context.Context, status vcs_clients.CommitState) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CommitStatus")
	defer span.End()

	ce.client.CommitStatus(ctx, ce.repo, status)
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
	// Argo diff logic wants unformatted manifests but everything else wants them as YAML, so we prepare both
	formattedManifests := argo_client.FormatManifestsYAML(manifests)
	if err != nil {
		ce.logger.Error().Err(err).Msg("Unable to get manifests")
		ce.vcsNote.AddToAppMessage(ctx, app, fmt.Sprintf("Unable to get manifests for application: \n\n ```\n%s\n```", ce.cleanupGetManifestsError(err)))
		return nil
	}
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
	grp.Go(func() error {
		const taskDescription = "validating app against schema"
		defer func() {
			if r := recover(); r != nil {
				telemetry.SetError(span, fmt.Errorf("%v", r), taskDescription)
				ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, taskDescription, r))
			}
		}()

		s, err := validate.ArgoCdAppValidate(grpCtx, app, k8sVersion, formattedManifests)
		if err != nil {
			telemetry.SetError(span, err, taskDescription)
			ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, taskDescription, err))
			return fmt.Errorf("argo Validate: %s", err)
		}

		if s != "" {
			ce.vcsNote.AddToAppMessage(grpCtx, app, s)
		}
		return nil

	})

	grp.Go(func() error {
		const taskDescription = "generating diff for app"
		defer func() {
			if r := recover(); r != nil {
				telemetry.SetError(span, fmt.Errorf("%v", r), taskDescription)
				ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, taskDescription, r))
			}
		}()

		s, rawDiff, err := diff.GetDiff(grpCtx, app, manifests)
		if err != nil {
			telemetry.SetError(span, err, taskDescription)
			ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, taskDescription, err))
			return fmt.Errorf("argo Diff: %s", err)
		}

		if s != "" {
			ce.vcsNote.AddToAppMessage(grpCtx, app, s)
			diff.AIDiffSummary(grpCtx, ce.vcsNote, app, manifests, rawDiff)
		}

		return nil
	})

	if viper.GetBool("enable-conftest") {
		grp.Go(func() error {
			const taskDescription = "validating app against policy"
			defer func() {
				if r := recover(); r != nil {
					telemetry.SetError(span, fmt.Errorf("%v", r), taskDescription)
					ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, taskDescription, r))
				}
			}()

			argoApp, err := argo_client.GetArgoClient().GetApplicationByName(grpCtx, app)
			if err != nil {
				telemetry.SetError(span, err, taskDescription)
				ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf("Could not retrieve Argo App details. %v", err))
				return fmt.Errorf("could not retrieve ArgoCD App data: %v", err)
			}

			s, err := conftest.Conftest(grpCtx, argoApp, ce.TempWorkingDir)
			if err != nil {
				telemetry.SetError(span, err, taskDescription)
				ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, taskDescription, err))
				return fmt.Errorf("confTest: %s", err)
			}

			if s != "" {
				ce.vcsNote.AddToAppMessage(grpCtx, app, s)
			}
			return nil
		})
	}

	grp.Go(func() error {
		const taskDescription = "running pre-upgrade check"
		defer func() {
			if r := recover(); r != nil {
				telemetry.SetError(span, fmt.Errorf("%v", r), taskDescription)
				ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, taskDescription, r))
			}
		}()

		s, err := kubepug.CheckApp(grpCtx, app, k8sVersion, formattedManifests)
		if err != nil {
			telemetry.SetError(span, err, taskDescription)
			ce.vcsNote.AddToAppMessage(grpCtx, app, fmt.Sprintf(errorCommentFormat, taskDescription, err))
			return fmt.Errorf("kubePug: %s", err)
		}

		if s != "" {
			ce.vcsNote.AddToAppMessage(grpCtx, app, s)
		}
		return nil

	})

	err = grp.Wait()
	if err != nil {
		telemetry.SetError(span, err, "running checks")
	}

	ce.vcsNote.AddToAppMessage(ctx, app, renderInfoFooter(time.Since(start), ce.repo.SHA))

	return err
}

// Creates a generic Note struct that we can write into across all worker threads
func (ce *CheckEvent) createNote(ctx context.Context) *vcs_clients.Message {
	ctx, span := otel.Tracer("check").Start(ctx, "createNote")
	defer span.End()

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Kubechecks Report:\n")
	ce.logger.Info().Msgf("Creating note")

	return ce.client.PostMessage(ctx, ce.repo.FullName, ce.repo.CheckID, sb.String())
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
