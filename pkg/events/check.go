package events

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
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

// AIReviewHardMaxApps is the absolute upper bound for AI reviews per MR/PR.
// Even if the user configures a higher value, this limit applies.
const AIReviewHardMaxApps = 50

// AIReviewChecker runs AI review for a single app.
// Defined here to avoid import cycles with pkg/checks/aireview.
type AIReviewChecker interface {
	Check(ctx context.Context, request checks.Request) (vcs.AIReviewResult, error)
	// AggregateReviews consolidates multiple per-app reviews into a single concise review.
	// Only called when multiple apps are reviewed. Returns consolidated text.
	AggregateReviews(ctx context.Context, appReviews map[string]string) (string, error)
}

// aiReviewAppResult holds the result of an AI review for a single app (used internally for aggregation).
type aiReviewAppResult struct {
	AppName     string
	Result      msg.Result
	Suggestions []vcs.ReviewSuggestion
}

type CheckEvent struct {
	fileList    []string // What files have changed in this PR/MR
	pullRequest vcs.PullRequest
	logger      zerolog.Logger
	vcsNote     *msg.Message
	aiNote      *msg.Message // separate comment for AI review

	affectedItems affected_apps.AffectedItems

	ctr             container.Container
	repoManager     repoManager
	processors      []checks.ProcessorEntry
	aiReviewChecker AIReviewChecker // runs separately, posts its own comment
	repoLock        sync.Mutex
	clonedRepos     map[repoKey]*git.Repo

	addedAppsSet     map[string]v1alpha1.Application
	addedAppsSetLock sync.Mutex

	aiReviewResults     []aiReviewAppResult
	aiReviewResultsLock sync.Mutex
	aiReviewCount       int32 // atomic counter for AI reviews claimed
	aiReviewSkipped     int32 // atomic counter for AI reviews skipped due to cap

	appsSent   int32
	appChannel chan *v1alpha1.Application
	wg         sync.WaitGroup
	generator  generator.AppsGenerator
	matcher    affected_apps.Matcher
}

type repoManager interface {
	Clone(ctx context.Context, cloneURL, branchName string) (*git.Repo, error)
}

func generateMatcher(ce *CheckEvent, repo *git.Repo) error {
	log.Debug().Caller().Msg("using the argocd matcher")
	m, err := affected_apps.NewArgocdMatcher(ce.ctr.VcsToArgoMap, repo)
	if err != nil {
		return errors.Wrap(err, "failed to create argocd matcher")
	}
	ce.matcher = m
	cfg, err := repo_config.LoadRepoConfig(repo.Directory)
	if err != nil {
		return errors.Wrap(err, "failed to load repo config")
	} else if cfg != nil {
		log.Debug().Caller().Msg("using the config matcher")
		configMatcher := affected_apps.NewConfigMatcher(cfg, ce.ctr)
		ce.matcher = affected_apps.NewMultiMatcher(ce.matcher, configMatcher)
	}
	return nil
}

func NewCheckEvent(pullRequest vcs.PullRequest, ctr container.Container, repoManager repoManager, processors []checks.ProcessorEntry, aiReviewChecker AIReviewChecker) *CheckEvent {

	ce := &CheckEvent{
		addedAppsSet:    make(map[string]v1alpha1.Application),
		appChannel:      make(chan *v1alpha1.Application, ctr.Config.MaxQueueSize),
		ctr:             ctr,
		clonedRepos:     make(map[repoKey]*git.Repo),
		processors:      processors,
		aiReviewChecker: aiReviewChecker,
		pullRequest:     pullRequest,
		repoManager:     repoManager,
		generator:       generator.New(),
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
		ce.logger.Error().Caller().Err(err).Msg("could not get list of affected apps and appsets")
	}
	for _, appSet := range ce.affectedItems.ApplicationSets {
		apps, err := ce.generator.GenerateApplicationSetApps(ctx, appSet, &ce.ctr)
		if err != nil {
			ce.logger.Error().Caller().Err(err).Msg("could not generate apps from appSet")
			continue
		}

		// Build a set of appset-generated app names for fast lookup.
		generatedNames := make(map[string]struct{}, len(apps))
		for _, a := range apps {
			generatedNames[a.Name] = struct{}{}
		}

		// Remove matcher-found apps that the appset generator also produced,
		// so the appset-generated version (which reflects PR template changes) wins.
		filtered := ce.affectedItems.Applications[:0]
		for _, existing := range ce.affectedItems.Applications {
			if _, ok := generatedNames[existing.Name]; ok {
				ce.logger.Debug().Caller().Msgf("replacing matcher app %s with appset-generated version", existing.Name)
				continue
			}
			filtered = append(filtered, existing)
		}
		ce.affectedItems.Applications = append(filtered, apps...)
	}

	span.SetAttributes(
		attribute.Int("numAffectedApps", len(ce.affectedItems.Applications)),
		attribute.Int("numAffectedAppSets", len(ce.affectedItems.ApplicationSets)),
		attribute.String("affectedApps", fmt.Sprintf("%+v", ce.affectedItems.Applications)),
		attribute.String("affectedAppSets", fmt.Sprintf("%+v", ce.affectedItems.ApplicationSets)),
	)
	for _, app := range ce.affectedItems.Applications {
		ce.logger.Debug().Caller().Msgf("Affected apps: %+v", app.Name)
	}
	for _, appset := range ce.affectedItems.ApplicationSets {
		ce.logger.Debug().Caller().Msgf("Affected appSets: %+v", appset.Name)
	}

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
	ce.logger.Info().Stack().Caller().Str("branchName", branchName).Msg("cloning repo")
	ce.repoLock.Lock()
	defer ce.repoLock.Unlock()

	parsed, err := canonicalize(cloneURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse clone url")
	}
	cloneURL = parsed.CloneURL(ce.ctr.VcsClient.CloneUsername())

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

func (ce *CheckEvent) Process(ctx context.Context) error {
	start := time.Now()

	_, span := tracer.Start(ctx, "GenerateListOfAffectedApps")
	defer span.End()

	var repo *git.Repo
	var err error

	// Archive mode: Download merge commit archive from VCS
	ce.logger.Info().Msg("using archive mode for PR processing")

	// Validate PR can be processed in archive mode (check for conflicts)
	if err = ce.ctr.ArchiveManager.ValidatePullRequest(ctx, ce.pullRequest); err != nil {
		ce.logger.Error().Caller().Err(err).Msg("Can not proceed with the download, PR/MR is not ready. (e.g. conflict, draft)")
		if postErr := ce.ctr.ArchiveManager.PostConflictMessage(ctx, ce.pullRequest); postErr != nil {
			ce.logger.Error().Caller().Err(postErr).Msg("failed to post conflict message")
		}
		return errors.Wrap(err, "Failed to validate pull request for archive processing")
	}

	// Download and extract archive (contains merged state)
	repo, err = ce.ctr.ArchiveManager.Clone(ctx, ce.pullRequest.CloneURL, ce.pullRequest.BaseRef, ce.pullRequest)
	if err != nil {
		return errors.Wrap(err, "failed to download archive")
	}

	// Store the archived repo in clonedRepos map so getRepo() can find it
	// This prevents workers from re-cloning with git
	parsed, err := canonicalize(ce.pullRequest.CloneURL)
	if err != nil {
		return errors.Wrap(err, "failed to canonicalize clone URL")
	}
	ce.repoLock.Lock()
	// Store under the HeadRef key (the branch being checked)
	ce.clonedRepos[generateRepoKey(parsed, ce.pullRequest.HeadRef)] = repo
	// Store under BaseRef if different
	if ce.pullRequest.HeadRef != ce.pullRequest.BaseRef {
		ce.clonedRepos[generateRepoKey(parsed, ce.pullRequest.BaseRef)] = repo
	}
	// IMPORTANT: Also store under "HEAD" key because ArgoCD often requests "HEAD"
	// which means "the default branch" (usually same as BaseRef)
	ce.clonedRepos[generateRepoKey(parsed, "HEAD")] = repo
	ce.logger.Debug().
		Caller().
		Str("head_ref", ce.pullRequest.HeadRef).
		Str("base_ref", ce.pullRequest.BaseRef).
		Msg("archived repo stored in clonedRepos under multiple keys (HeadRef, BaseRef, HEAD)")
	ce.repoLock.Unlock()

	// Get changed files from VCS API (replaces git diff)
	ce.fileList, err = ce.ctr.ArchiveManager.GetChangedFiles(ctx, ce.pullRequest)
	if err != nil {
		return errors.Wrap(err, "failed to get changed files from VCS API")
	}
	ce.logger.Info().
		Int("file_count", len(ce.fileList)).
		Str("files", strings.Join(ce.fileList, ", ")).
		Msg("Changed files retrieved from VCS API (archive mode)")

	// Generate a list of affected apps, storing them within the CheckEvent (also returns but discarded here)
	if err = ce.GenerateListOfAffectedApps(ctx, repo, ce.pullRequest.BaseRef, generateMatcher); err != nil {
		return errors.Wrap(err, "failed to generate a list of affected apps")
	}

	if err = ce.ctr.VcsClient.TidyOutdatedComments(ctx, ce.pullRequest); err != nil {
		ce.logger.Error().Caller().Err(err).Msg("Failed to tidy outdated comments")
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

	// Create a separate placeholder comment for AI review
	if ce.ctr.Config.EnableAIReview {
		ce.aiNote, err = ce.createAIReviewNote(ctx)
		if err != nil {
			ce.logger.Warn().Caller().Err(err).Msg("failed to create AI review note, AI review will be skipped")
			ce.aiReviewChecker = nil // disable AI review for this run
		}
	}

	for num := 0; num <= ce.ctr.Config.MaxConcurrentChecks; num++ {

		w := worker{
			appChannel:      ce.appChannel,
			ctr:             ce.ctr,
			logger:          ce.logger.With().Int("workerID", num).Logger(),
			pullRequest:     ce.pullRequest,
			processors:      ce.processors,
			aiReviewChecker: ce.aiReviewChecker,
			vcsNote:         ce.vcsNote,
			changedFiles:    ce.fileList,

			done:              ce.wg.Done,
			getRepo:           ce.getRepo,
			queueApp:          ce.queueApp,
			removeApp:         ce.removeApp,
			addAIReviewResult: ce.addAIReviewResult,
			claimAIReviewSlot: ce.claimAIReviewSlot,
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

	ce.logger.Debug().
		Caller().
		Int("all apps", len(ce.addedAppsSet)).
		Int32("sent apps", ce.appsSent).
		Msg("completed apps")

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

	// Update the AI review comment with aggregated results
	if ce.aiNote != nil {
		aiComment, aiWorstState, suggestions := ce.buildAIReviewComment(ctx)
		if err = ce.ctr.VcsClient.UpdateMessage(ctx, ce.aiNote, aiComment); err != nil {
			ce.logger.Error().Caller().Err(err).Msg("failed to update AI review comment")
		}
		// Post code suggestions as a separate review with inline comments
		if len(suggestions) > 0 {
			if err = ce.ctr.VcsClient.PostReviewSuggestions(ctx, ce.pullRequest, fmt.Sprintf("## Kubechecks %s AI Suggestion Report ##", ce.ctr.Config.Identifier), suggestions); err != nil {
				ce.logger.Error().Caller().Err(err).Msg("failed to post AI review suggestions")
			}
		}
		// Factor AI review state into overall commit status
		cappedAIState := pkg.BestState(aiWorstState, ce.ctr.Config.WorstAIReviewState)
		worstStatus = pkg.WorstState(worstStatus, cappedAIState)
	}

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

	logger.Debug().Caller().Msg("producing app on channel")
	ce.appChannel <- &app
	logger.Debug().Caller().Msg("finished producing app")
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

// createAIReviewNote creates the initial placeholder comment for the AI review.
func (ce *CheckEvent) createAIReviewNote(ctx context.Context) (*msg.Message, error) {
	ctx, span := otel.Tracer("check").Start(ctx, "createAIReviewNote")
	defer span.End()

	ce.logger.Info().Msg("Creating AI review note")

	return ce.ctr.VcsClient.PostMessage(ctx, ce.pullRequest, fmt.Sprintf("## Kubechecks %s Report — AI Review\n:hourglass: AI review running...", ce.ctr.Config.Identifier))
}

// effectiveAIReviewMax returns the effective AI review cap, clamped to the hard limit.
func (ce *CheckEvent) effectiveAIReviewMax() int {
	max := ce.ctr.Config.AIReviewMaxApps
	if max <= 0 {
		max = AIReviewHardMaxApps
	}
	if max > AIReviewHardMaxApps {
		max = AIReviewHardMaxApps
	}
	return max
}

// claimAIReviewSlot atomically claims an AI review slot. Returns true if under the cap.
func (ce *CheckEvent) claimAIReviewSlot() bool {
	n := int(atomic.AddInt32(&ce.aiReviewCount, 1))
	if n > ce.effectiveAIReviewMax() {
		atomic.AddInt32(&ce.aiReviewSkipped, 1)
		return false
	}
	return true
}

// addAIReviewResult collects an AI review result (thread-safe).
func (ce *CheckEvent) addAIReviewResult(appName string, result msg.Result, suggestions []vcs.ReviewSuggestion) {
	ce.aiReviewResultsLock.Lock()
	defer ce.aiReviewResultsLock.Unlock()
	ce.aiReviewResults = append(ce.aiReviewResults, aiReviewAppResult{AppName: appName, Result: result, Suggestions: suggestions})
}

// buildAIReviewComment aggregates all AI review results into a single comment and collects suggestions.
// When multiple apps are reviewed, runs the aggregator LLM to consolidate duplicate findings.
func (ce *CheckEvent) buildAIReviewComment(ctx context.Context) (string, pkg.CommitState, []vcs.ReviewSuggestion) {
	ce.aiReviewResultsLock.Lock()
	defer ce.aiReviewResultsLock.Unlock()

	header := fmt.Sprintf("## Kubechecks %s Report — AI Review\n\n", ce.ctr.Config.Identifier)

	if len(ce.aiReviewResults) == 0 {
		return fmt.Sprintf("## Kubechecks %s Report — AI Review\nNo review results.", ce.ctr.Config.Identifier), pkg.StateNone, nil
	}

	// Collect worst state and deduplicated suggestions
	worstState := pkg.StateNone
	var allSuggestions []vcs.ReviewSuggestion
	seen := make(map[string]bool)
	appReviews := make(map[string]string)
	for _, r := range ce.aiReviewResults {
		appReviews[r.AppName] = r.Result.Details
		worstState = pkg.WorstState(worstState, r.Result.State)
		for _, s := range r.Suggestions {
			key := fmt.Sprintf("%s:%d:%d:%s", s.Path, s.StartLine, s.EndLine, s.Suggestion)
			if seen[key] {
				continue
			}
			seen[key] = true
			allSuggestions = append(allSuggestions, s)
		}
	}

	// If multiple apps, run aggregator to consolidate findings
	var reviewBody string
	if len(appReviews) > 1 && ce.ctr.Config.EnableAIReview && ce.aiReviewChecker != nil {
		consolidated, err := ce.aiReviewChecker.AggregateReviews(ctx, appReviews)
		if err != nil {
			ce.logger.Warn().Caller().Err(err).Msg("aggregation failed, using raw reviews")
			reviewBody = buildRawReviewBody(appReviews)
		} else {
			reviewBody = consolidated
		}
	} else {
		reviewBody = buildRawReviewBody(appReviews)
	}

	// Append cap notice if any apps were skipped
	skipped := int(atomic.LoadInt32(&ce.aiReviewSkipped))
	if skipped > 0 {
		totalApps := len(ce.affectedItems.Applications)
		reviewed := len(ce.aiReviewResults)
		effectiveMax := ce.effectiveAIReviewMax()
		capType := "configured limit"
		if effectiveMax == AIReviewHardMaxApps && ce.ctr.Config.AIReviewMaxApps > AIReviewHardMaxApps {
			capType = "hard limit"
		}
		reviewBody += fmt.Sprintf(
			"\n---\n:warning: **AI review cap reached** — reviewed %d of %d affected apps (%s: %d). %d apps were skipped.\n",
			reviewed, totalApps, capType, effectiveMax, skipped,
		)
	}

	return header + reviewBody, worstState, allSuggestions
}

// buildRawReviewBody concatenates per-app reviews without aggregation, wrapped in <details> tags.
// App names are sorted for stable output across runs.
func buildRawReviewBody(appReviews map[string]string) string {
	names := make([]string, 0, len(appReviews))
	for name := range appReviews {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	for _, appName := range names {
		fmt.Fprintf(&sb, "<details>\n<summary><code>%s</code></summary>\n\n%s\n\n</details>\n\n", appName, appReviews[appName])
	}
	return sb.String()
}
