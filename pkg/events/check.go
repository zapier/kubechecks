package events

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/affected_apps"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/repo_config"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/events")

type CheckEvent struct {
	fileList     []string // What files have changed in this PR/MR
	pullRequest  vcs.PullRequest
	logger       zerolog.Logger
	workerLimits int
	vcsNote      *msg.Message

	affectedItems affected_apps.AffectedItems

	ctr         container.Container
	repoManager repoManager
	processors  []checks.ProcessorEntry
	repoLock    sync.Mutex
	clonedRepos map[string]*git.Repo

	addedAppsSet map[string]v1alpha1.Application
	appsSent     int32
	appChannel   chan *v1alpha1.Application
	wg           sync.WaitGroup
}

type repoManager interface {
	Clone(ctx context.Context, cloneURL, branchName string) (*git.Repo, error)
}

func NewCheckEvent(pullRequest vcs.PullRequest, ctr container.Container, repoManager repoManager, processors []checks.ProcessorEntry) *CheckEvent {
	ce := &CheckEvent{
		ctr:         ctr,
		clonedRepos: make(map[string]*git.Repo),
		processors:  processors,
		pullRequest: pullRequest,
		repoManager: repoManager,
		logger: log.Logger.With().
			Str("repo", pullRequest.Name).
			Int("event_id", pullRequest.CheckID).
			Logger(),
	}

	return ce
}

func (ce *CheckEvent) UpdateListOfChangedFiles(ctx context.Context, repo *git.Repo) error {
	ctx, span := tracer.Start(ctx, "CheckEventGetListOfChangedFiles")
	defer span.End()

	files, err := repo.GetListOfChangedFiles(ctx)
	if err != nil {
		return err
	}

	ce.logger.Debug().Msgf("Changed files: %s", strings.Join(files, ","))
	ce.fileList = files
	return nil
}

// GenerateListOfAffectedApps walks the repo to find any apps or appsets impacted by the changes in the MR/PR.
func (ce *CheckEvent) GenerateListOfAffectedApps(ctx context.Context, repo *git.Repo, targetBranch string) error {
	_, span := tracer.Start(ctx, "GenerateListOfAffectedApps")
	defer span.End()
	var err error

	var matcher affected_apps.Matcher
	cfg, _ := repo_config.LoadRepoConfig(repo.Directory)
	if cfg != nil {
		log.Debug().Msg("using the config matcher")
		matcher = affected_apps.NewConfigMatcher(cfg, ce.ctr)
	} else {
		log.Debug().Msg("using an argocd matcher")
		matcher, err = affected_apps.NewArgocdMatcher(ce.ctr.VcsToArgoMap, repo)
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

func canonicalize(cloneURL string) (pkg.RepoURL, error) {
	parsed, _, err := pkg.NormalizeRepoUrl(cloneURL)
	if err != nil {
		return pkg.RepoURL{}, errors.Wrap(err, "failed to parse clone url")
	}

	return parsed, nil
}

func generateRepoKey(cloneURL pkg.RepoURL, branchName string) string {
	return fmt.Sprintf("%s|||%s", cloneURL.CloneURL(""), branchName)
}

type hasUsername interface {
	Username() string
}

func (ce *CheckEvent) getRepo(ctx context.Context, vcsClient hasUsername, cloneURL, branchName string) (*git.Repo, error) {
	var (
		err  error
		repo *git.Repo
	)

	ce.repoLock.Lock()
	defer ce.repoLock.Unlock()

	parsed, err := canonicalize(cloneURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse clone url")
	}
	cloneURL = parsed.CloneURL(vcsClient.Username())

	branchName = strings.TrimSpace(branchName)
	if branchName == "" {
		branchName = "HEAD"
	}
	reposKey := generateRepoKey(parsed, branchName)

	if repo, ok := ce.clonedRepos[reposKey]; ok {
		return repo, nil
	}

	repo, err = ce.repoManager.Clone(ctx, cloneURL, branchName)
	if err != nil {
		return nil, errors.Wrap(err, "failed to clone repo")
	}

	ce.clonedRepos[reposKey] = repo

	// if we cloned 'HEAD', figure out its original branch and store a copy of the repo there
	if branchName == "" || branchName == "HEAD" {
		remoteHeadBranchName, err := repo.GetRemoteHead()
		if err != nil {
			return repo, errors.Wrap(err, "failed to determine remote head")
		}

		repo.BranchName = remoteHeadBranchName
		ce.clonedRepos[generateRepoKey(parsed, remoteHeadBranchName)] = repo
	}

	// if we don't have a 'HEAD' saved for the cloned repo, figure out which branch HEAD points to,
	// and if it's the one we just cloned, store a copy of it as 'HEAD' for usage later
	headKey := generateRepoKey(parsed, "HEAD")
	if _, ok := ce.clonedRepos[headKey]; !ok {
		remoteHeadBranchName, err := repo.GetRemoteHead()
		if err != nil {
			return repo, errors.Wrap(err, "failed to determine remote head")
		}
		if remoteHeadBranchName == repo.BranchName {
			ce.clonedRepos[headKey] = repo
		}
	}

	return repo, nil
}

func (ce *CheckEvent) Process(ctx context.Context) error {
	_, span := tracer.Start(ctx, "GenerateListOfAffectedApps")
	defer span.End()

	// Clone the repo's BaseRef (main etc) locally into the temp dir we just made
	repo, err := ce.getRepo(ctx, ce.ctr.VcsClient, ce.pullRequest.CloneURL, ce.pullRequest.BaseRef)
	if err != nil {
		return errors.Wrap(err, "failed to clone repo")
	}

	// Merge the most recent changes into the branch we just cloned
	if err = repo.MergeIntoTarget(ctx, ce.pullRequest.SHA); err != nil {
		return errors.Wrap(err, "failed to merge into target")
	}

	// Get the diff between the two branches, storing them within the CheckEvent (also returns but discarded here)
	if err = ce.UpdateListOfChangedFiles(ctx, repo); err != nil {
		return errors.Wrap(err, "failed to get list of changed files")
	}

	// Generate a list of affected apps, storing them within the CheckEvent (also returns but discarded here)
	if err = ce.GenerateListOfAffectedApps(ctx, repo, ce.pullRequest.BaseRef); err != nil {
		return errors.Wrap(err, "failed to generate a list of affected apps")
	}

	if err = ce.ctr.VcsClient.TidyOutdatedComments(ctx, ce.pullRequest); err != nil {
		ce.logger.Error().Err(err).Msg("Failed to tidy outdated comments")
	}

	if len(ce.affectedItems.Applications) <= 0 && len(ce.affectedItems.ApplicationSets) <= 0 {
		ce.logger.Info().Msg("No affected apps or appsets, skipping")
		ce.ctr.VcsClient.PostMessage(ctx, ce.pullRequest, "No changes")
		return nil
	}

	// Concurrently process all apps, with a corresponding error channel for reporting back failures
	ce.addedAppsSet = make(map[string]v1alpha1.Application)
	ce.appChannel = make(chan *v1alpha1.Application, len(ce.affectedItems.Applications)*2)

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

	ce.wg.Wait()

	close(ce.appChannel)

	ce.logger.Debug().Msg("finished an app")

	ce.logger.Debug().
		Int("all apps", len(ce.addedAppsSet)).
		Int32("sent apps", ce.appsSent).
		Msg("completed apps")

	ce.logger.Debug().Msg("Closing channels")

	ce.logger.Info().Msg("Finished")

	comment := ce.vcsNote.BuildComment(ctx)
	if err = ce.ctr.VcsClient.UpdateMessage(ctx, ce.vcsNote, comment); err != nil {
		return errors.Wrap(err, "failed to push comment")
	}

	worstStatus := ce.vcsNote.WorstState()
	ce.CommitStatus(ctx, worstStatus)

	return nil
}

func (ce *CheckEvent) removeApp(app v1alpha1.Application) {
	ce.vcsNote.RemoveApp(app.Name)
}

func (ce *CheckEvent) queueApp(app v1alpha1.Application) {
	name := app.Name
	dir := app.Spec.GetSource().Path

	if old, ok := ce.addedAppsSet[name]; ok {
		if reflect.DeepEqual(old, app) {
			return
		}
	}

	ce.addedAppsSet[name] = app

	logger := ce.logger.Debug().
		Str("app", name).
		Str("dir", dir).
		Str("cluster-name", app.Spec.Destination.Name).
		Str("cluster-server", app.Spec.Destination.Server)

	ce.wg.Add(1)
	atomic.AddInt32(&ce.appsSent, 1)

	logger.Msg("producing app on channel")
	ce.appChannel <- &app
	logger.Msg("finished producing app")
}

// CommitStatus sets the commit status on the MR
// To set the PR/MR status
func (ce *CheckEvent) CommitStatus(ctx context.Context, status pkg.CommitState) {
	_, span := tracer.Start(ctx, "CommitStatus")
	defer span.End()

	if err := ce.ctr.VcsClient.CommitStatus(ctx, ce.pullRequest, status); err != nil {
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

		ce.wg.Done()
	}
}

// processApp is a function that validates and processes a given application manifest against various checks,
// such as ArgoCD schema validation, diff generation, conftest policy validation, and pre-upgrade checks using kubepug.
// It takes a context (ctx), application name (app), directory (dir) as input and returns an error if any check fails.
// The processing is performed concurrently using Go routines and error groups. Any check results are sent through
// the returnChan. The function also manages the inFlight atomic counter to track active processing routines.
func (ce *CheckEvent) processApp(ctx context.Context, app v1alpha1.Application) {
	var (
		err error

		appName    = app.Name
		appSrc     = app.Spec.Source
		appPath    = appSrc.Path
		appRepoUrl = appSrc.RepoURL

		logger = ce.logger.With().
			Str("app_name", appName).
			Str("app_path", appPath).
			Logger()
	)

	ctx, span := tracer.Start(ctx, "processApp", trace.WithAttributes(
		attribute.String("app", appName),
		attribute.String("dir", appPath),
	))
	defer span.End()

	atomic.AddInt32(&inFlight, 1)
	defer atomic.AddInt32(&inFlight, -1)

	start := time.Now()
	logger.Info().Str("app", appName).Msg("Adding new app")
	// Build a new section for this app in the parent comment
	ce.vcsNote.AddNewApp(ctx, appName)

	repo, err := ce.getRepo(ctx, ce.ctr.VcsClient, appRepoUrl, appSrc.TargetRevision)
	if err != nil {
		logger.Error().Err(err).Msg("Unable to clone repository")
		ce.vcsNote.AddToAppMessage(ctx, appName, msg.Result{
			State:   pkg.StateError,
			Summary: "failed to clone repo",
			Details: fmt.Sprintf("Clone URL: `%s`\nTarget Revision: `%s`\n```\n%s\n```", appRepoUrl, appSrc.TargetRevision, err.Error()),
		})
		return
	}
	repoPath := repo.Directory

	logger.Debug().Str("repo_path", repoPath).Msg("Getting manifests")
	jsonManifests, err := argo_client.GetManifestsLocal(ctx, ce.ctr.ArgoClient, appName, repoPath, appPath, app)
	if err != nil {
		logger.Error().Err(err).Msg("Unable to get manifests")
		ce.vcsNote.AddToAppMessage(ctx, appName, msg.Result{
			State:   pkg.StateError,
			Summary: "Unable to get manifests",
			Details: fmt.Sprintf("```\n%s\n```", cleanupGetManifestsError(err, repo.Directory)),
		})
		return
	}

	// Argo diff logic wants unformatted manifests but everything else wants them as YAML, so we prepare both
	yamlManifests := argo_client.ConvertJsonToYamlManifests(jsonManifests)
	logger.Trace().Msgf("Manifests:\n%+v\n", yamlManifests)

	k8sVersion, err := ce.ctr.ArgoClient.GetKubernetesVersionByApplication(ctx, app)
	if err != nil {
		logger.Error().Err(err).Msg("Error retrieving the Kubernetes version")
		k8sVersion = ce.ctr.Config.FallbackK8sVersion
	} else {
		k8sVersion = fmt.Sprintf("%s.0", k8sVersion)
		logger.Info().Msgf("Kubernetes version: %s", k8sVersion)
	}

	runner := newRunner(
		ce.ctr, app, repo, appName, k8sVersion, jsonManifests, yamlManifests, logger, ce.vcsNote,
		ce.queueApp, ce.removeApp,
	)

	for _, processor := range ce.processors {
		runner.Run(ctx, processor.Name, processor.Processor, processor.WorstState)
	}

	runner.Wait()

	ce.vcsNote.SetFooter(start, ce.pullRequest.SHA, ce.ctr.Config.LabelFilter, ce.ctr.Config.ShowDebugInfo)
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

// Creates a generic Note struct that we can write into across all worker threads
func (ce *CheckEvent) createNote(ctx context.Context) *msg.Message {
	ctx, span := otel.Tracer("check").Start(ctx, "createNote")
	defer span.End()

	ce.logger.Info().Msgf("Creating note")

	return ce.ctr.VcsClient.PostMessage(ctx, ce.pullRequest, ":hourglass: kubechecks running ... ")
}

// cleanupGetManifestsError takes an error as input and returns a simplified and more user-friendly error message.
// It reformats Helm error messages by removing excess information, and makes file paths relative to the git repo root.
func cleanupGetManifestsError(err error, repoDirectory string) string {
	// cleanup the chonky helm error message for a better DX
	errStr := err.Error()
	if strings.Contains(errStr, "helm template") && strings.Contains(errStr, "failed exit status") {
		errMsgIdx := strings.Index(errStr, "Error:")
		errStr = fmt.Sprintf("Helm %s", errStr[errMsgIdx:])
	}

	// strip the temp directory from any files mentioned to make file paths relative to git repo root
	errStr = strings.ReplaceAll(errStr, repoDirectory+"/", "")

	return errStr
}
