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
	"github.com/zapier/kubechecks/pkg/generator"
	"github.com/zapier/kubechecks/pkg/repo_config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/affected_apps"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/events")

type CheckEvent struct {
	fileList    []string // What files have changed in this PR/MR
	pullRequest vcs.PullRequest
	logger      zerolog.Logger
	vcsNote     *msg.Message

	affectedItems affected_apps.AffectedItems

	ctr         container.Container
	repoManager repoManager
	processors  []checks.ProcessorEntry
	repoLock    sync.Mutex
	clonedRepos map[repoKey]*git.Repo

	addedAppsSet     map[string]v1alpha1.Application
	addedAppsSetLock sync.Mutex

	appsSent   int32
	appChannel chan *v1alpha1.Application
	wg         sync.WaitGroup
	generator  generator.AppsGenerator
	matcher    affected_apps.Matcher
}

type repoManager interface {
	Clone(ctx context.Context, cloneURL, branchName string, shallow bool) (*git.Repo, error)
}

func generateMatcher(ce *CheckEvent, repo *git.Repo) error {
	log.Debug().Msg("using the argocd matcher")
	m, err := affected_apps.NewArgocdMatcher(ce.ctr.VcsToArgoMap, repo)
	if err != nil {
		return errors.Wrap(err, "failed to create argocd matcher")
	}
	ce.matcher = m
	cfg, err := repo_config.LoadRepoConfig(repo.Directory)
	if err != nil {
		return errors.Wrap(err, "failed to load repo config")
	} else if cfg != nil {
		log.Debug().Msg("using the config matcher")
		configMatcher := affected_apps.NewConfigMatcher(cfg, ce.ctr)
		ce.matcher = affected_apps.NewMultiMatcher(ce.matcher, configMatcher)
	}
	return nil
}

func NewCheckEvent(pullRequest vcs.PullRequest, ctr container.Container, repoManager repoManager, processors []checks.ProcessorEntry) *CheckEvent {

	ce := &CheckEvent{
		addedAppsSet: make(map[string]v1alpha1.Application),
		appChannel:   make(chan *v1alpha1.Application, ctr.Config.MaxQueueSize),
		ctr:          ctr,
		clonedRepos:  make(map[repoKey]*git.Repo),
		processors:   processors,
		pullRequest:  pullRequest,
		repoManager:  repoManager,
		generator:    generator.New(),
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

type MatcherFn func(ce *CheckEvent, repo *git.Repo) error

// GenerateListOfAffectedApps walks the repo to find any apps or appsets impacted by the changes in the MR/PR.
func (ce *CheckEvent) GenerateListOfAffectedApps(ctx context.Context, repo *git.Repo, targetBranch string, initMatcherFn MatcherFn) error {
	_, span := tracer.Start(ctx, "GenerateListOfAffectedApps")
	defer span.End()
	var err error

	err = initMatcherFn(ce, repo)
	if err != nil {
		return errors.Wrap(err, "failed to create argocd matcher")
	}

	// use the changed file path to get the list of affected apps
	// fileList is a list of changed files in the PR/MR, e.g. ["path/to/file1", "path/to/file2"]
	ce.affectedItems, err = ce.matcher.AffectedApps(ctx, ce.fileList, targetBranch, repo)
	if err != nil {
		telemetry.SetError(span, err, "Get Affected Apps")
		ce.logger.Error().Err(err).Msg("could not get list of affected apps and appsets")
	}
	for _, appSet := range ce.affectedItems.ApplicationSets {
		apps, err := ce.generator.GenerateApplicationSetApps(ctx, appSet, &ce.ctr)
		if err != nil {
			ce.logger.Error().Err(err).Msg("could not generate apps from appSet")
			continue
		}
		ce.affectedItems.Applications = append(ce.affectedItems.Applications, apps...)
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

type repoKey string

func generateRepoKey(cloneURL pkg.RepoURL, branchName string) repoKey {
	key := fmt.Sprintf("%s|||%s", cloneURL.CloneURL(""), branchName)
	return repoKey(key)
}

func (ce *CheckEvent) getRepo(ctx context.Context, cloneURL, branchName string) (*git.Repo, error) {
	var (
		err  error
		repo *git.Repo
	)
	ce.logger.Info().Stack().Str("branchName", branchName).Msg("cloning repo")
	ce.repoLock.Lock()
	defer ce.repoLock.Unlock()

	parsed, err := canonicalize(cloneURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse clone url")
	}
	cloneURL = parsed.CloneURL(ce.ctr.VcsClient.Username())

	branchName = strings.TrimSpace(branchName)
	if branchName == "" {
		branchName = "HEAD"
	}
	reposKey := generateRepoKey(parsed, branchName)

	if repo, ok := ce.clonedRepos[reposKey]; ok {
		return repo, nil
	}

	repo, err = ce.repoManager.Clone(ctx, cloneURL, branchName, ce.ctr.Config.RepoShallowClone)
	if err != nil {
		return nil, errors.Wrap(err, "failed to clone repo")
	}

	ce.clonedRepos[reposKey] = repo

	// if we cloned 'HEAD', figure out its original branch and store a copy of the repo there
	if branchName == "HEAD" {
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

func (ce *CheckEvent) mergeIntoTarget(ctx context.Context, repo *git.Repo, branch string) error {
	if err := repo.MergeIntoTarget(ctx, fmt.Sprintf("origin/%s", branch)); err != nil {
		return errors.Wrap(err, "failed to merge into target")
	}

	parsed, err := canonicalize(repo.CloneURL)
	if err != nil {
		return errors.Wrap(err, "failed to canonicalize url")
	}

	reposKey := generateRepoKey(parsed, branch)
	ce.clonedRepos[reposKey] = repo

	return nil
}

func (ce *CheckEvent) Process(ctx context.Context) error {
	start := time.Now()

	_, span := tracer.Start(ctx, "GenerateListOfAffectedApps")
	defer span.End()

	// Clone the repo's BaseRef (main, etc.) locally into the temp dir we just made
	repo, err := ce.getRepo(ctx, ce.pullRequest.CloneURL, ce.pullRequest.BaseRef)
	if err != nil {
		return errors.Wrap(err, "failed to clone repo")
	}

	// Merge the most recent changes into the branch we just cloned
	if err = ce.mergeIntoTarget(ctx, repo, ce.pullRequest.HeadRef); err != nil {
		return errors.Wrap(err, "failed to merge into target")
	}

	// Get the diff between the two branches, storing them within the CheckEvent (also returns but discarded here)
	if err = ce.UpdateListOfChangedFiles(ctx, repo); err != nil {
		return errors.Wrap(err, "failed to get list of changed files")
	}

	// Generate a list of affected apps, storing them within the CheckEvent (also returns but discarded here)
	if err = ce.GenerateListOfAffectedApps(ctx, repo, ce.pullRequest.BaseRef, generateMatcher); err != nil {
		return errors.Wrap(err, "failed to generate a list of affected apps")
	}

	if err = ce.ctr.VcsClient.TidyOutdatedComments(ctx, ce.pullRequest); err != nil {
		ce.logger.Error().Err(err).Msg("Failed to tidy outdated comments")
	}

	if len(ce.affectedItems.Applications) <= 0 && len(ce.affectedItems.ApplicationSets) <= 0 {
		ce.logger.Info().Msg("No affected apps or appsets, skipping")
		if _, err := ce.ctr.VcsClient.PostMessage(ctx, ce.pullRequest, fmt.Sprintf("## Kubechecks %s Report\nNo changes", ce.ctr.Config.Identifier)); err != nil {
			return errors.Wrap(err, "failed to post changes")
		}
		return nil
	}

	// We make one comment per run, containing output for all the apps
	ce.vcsNote, err = ce.createNote(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create note")
	}

	for num := 0; num <= ce.ctr.Config.MaxConcurrenctChecks; num++ {

		w := worker{
			appChannel:  ce.appChannel,
			ctr:         ce.ctr,
			logger:      ce.logger.With().Int("workerID", num).Logger(),
			pullRequest: ce.pullRequest,
			processors:  ce.processors,
			vcsNote:     ce.vcsNote,

			done:      ce.wg.Done,
			getRepo:   ce.getRepo,
			queueApp:  ce.queueApp,
			removeApp: ce.removeApp,
		}
		go w.run(ctx)
	}
	ce.logger.Info().Msgf("adding %d apps to the queue", len(ce.affectedItems.Applications))
	// Produce apps onto channel
	for _, app := range ce.affectedItems.Applications {
		ce.queueApp(app)
	}

	ce.wg.Wait()

	close(ce.appChannel)

	ce.logger.Debug().Msg("finished an app/appsets")

	ce.logger.Debug().
		Int("all apps", len(ce.addedAppsSet)).
		Int32("sent apps", ce.appsSent).
		Msg("completed apps")

	ce.logger.Debug().Msg("Closing channels")

	ce.logger.Info().Msg("Finished")

	comment := ce.vcsNote.BuildComment(
		ctx, start, ce.pullRequest.SHA, ce.ctr.Config.LabelFilter,
		ce.ctr.Config.ShowDebugInfo, ce.ctr.Config.Identifier,
		len(ce.addedAppsSet), int(ce.appsSent),
	)

	if err = ce.ctr.VcsClient.UpdateMessage(ctx, ce.vcsNote, comment); err != nil {
		return errors.Wrap(err, "failed to push comment")
	}

	worstStatus := ce.vcsNote.WorstState()
	ce.CommitStatus(ctx, worstStatus)

	return nil
}

func (ce *CheckEvent) removeApp(app v1alpha1.Application) {
	ce.logger.Info().Str("app", app.Name).Msg("removing app")

	ce.vcsNote.RemoveApp(app.Name)
}

func (ce *CheckEvent) queueApp(app v1alpha1.Application) {
	ce.addedAppsSetLock.Lock()
	defer ce.addedAppsSetLock.Unlock()

	name := app.Name
	dir := app.Spec.GetSource().Path

	if old, ok := ce.addedAppsSet[name]; ok {
		if reflect.DeepEqual(old, app) {
			return
		}
	}

	ce.addedAppsSet[name] = app

	logger := ce.logger.With().
		Str("app", name).
		Str("dir", dir).
		Str("cluster-name", app.Spec.Destination.Name).
		Str("cluster-server", app.Spec.Destination.Server).
		Logger()
	logger.Info().Msg("adding app for processing")

	ce.wg.Add(1)
	atomic.AddInt32(&ce.appsSent, 1)

	logger.Debug().Msg("producing app on channel")
	ce.appChannel <- &app
	logger.Debug().Msg("finished producing app")
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

const (
	errorCommentFormat = `
:warning:  **Error while %s** :warning:
` + "```" + `
%v
` + "```" + `

Check kubechecks application logs for more information.
`
)

// createNote creates a generic Note struct that we can write into across all worker threads
func (ce *CheckEvent) createNote(ctx context.Context) (*msg.Message, error) {
	ctx, span := otel.Tracer("check").Start(ctx, "createNote")
	defer span.End()

	ce.logger.Info().Msgf("Creating note")

	return ce.ctr.VcsClient.PostMessage(ctx, ce.pullRequest, fmt.Sprintf("## Kubechecks %s Report\n:hourglass: kubechecks running...", ce.ctr.Config.Identifier))
}
