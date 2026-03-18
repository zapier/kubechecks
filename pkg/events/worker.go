package events

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/ghodss/yaml"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

type worker struct {
	appChannel      chan *v1alpha1.Application
	ctr             container.Container
	logger          zerolog.Logger
	processors      []checks.ProcessorEntry
	aiReviewChecker AIReviewChecker
	pullRequest     vcs.PullRequest
	vcsNote         *msg.Message

	done                func()
	getRepo             func(ctx context.Context, cloneURL, branchName string) (*git.Repo, error)
	queueApp, removeApp func(application v1alpha1.Application)
	addAIReviewResult   func(appName string, result msg.Result)
}

// process apps
func (w *worker) run(ctx context.Context) {
	for app := range w.appChannel {
		if app != nil {
			w.logger.Info().Str("app", app.Name).Msg("Processing App")
			w.processApp(ctx, *app)
		} else {
			w.logger.Warn().Msg("appWorkers received a nil app")
		}

		w.done()
	}
}

// processApp is a function that validates and processes a given application manifest against various checks,
// such as ArgoCD schema validation, diff generation, conftest policy validation, and pre-upgrade checks using kubepug.
// It takes a context (ctx), application name (app), directory (dir) as input and returns an error if any check fails.
// The processing is performed concurrently using Go routines and error groups. Any check results are sent through
// the returnChan. The function also manages the inFlight atomic counter to track active processing routines.
func (w *worker) processApp(ctx context.Context, app v1alpha1.Application) {
	var (
		err error

		appName = app.Name

		rootLogger = w.logger.With().
				Str("app_name", appName).
				Logger()
	)

	ctx, span := tracer.Start(ctx, "processApp", trace.WithAttributes(
		attribute.String("app", appName),
	))
	defer span.End()

	atomic.AddInt32(&inFlight, 1)
	defer atomic.AddInt32(&inFlight, -1)

	rootLogger.Info().Msg("Processing app")

	// Build a new section for this app in the parent comment
	w.vcsNote.AddNewApp(ctx, appName)

	defer func() {
		if r := recover(); r != nil {
			desc := fmt.Sprintf("panic while checking %s", appName)
			w.logger.Error().Caller().Any("error", r).
				Str("app", appName).Msgf("panic while running check")
			println(string(debug.Stack()))

			telemetry.SetError(span, fmt.Errorf("%v", r), "panic while running check")
			result := msg.Result{
				State:   pkg.StatePanic,
				Summary: desc,
				Details: fmt.Sprintf(errorCommentFormat, desc, r),
			}
			w.vcsNote.AddToAppMessage(ctx, appName, result)
		}
	}()

	rootLogger.Debug().Caller().Msg("Getting manifests")
	jsonManifests, err := w.ctr.ArgoClient.GetManifests(ctx, appName, app, w.pullRequest, w.getRepo)
	if err != nil {
		rootLogger.Error().Caller().Err(err).Str("app", appName).Str("repo", w.pullRequest.Name).Msg("Unable to get manifests")
		w.vcsNote.AddToAppMessage(ctx, appName, msg.Result{
			State:   pkg.StateError,
			Summary: "Unable to get manifests",
			Details: fmt.Sprintf("```\n%s\n```", err),
		})
		return
	}

	// Argo diff logic wants unformatted manifests but everything else wants them as YAML, so we prepare both
	yamlManifests := convertJsonToYamlManifests(jsonManifests)
	rootLogger.Trace().Msgf("Manifests:\n%+v\n", yamlManifests)

	k8sVersion, err := w.ctr.ArgoClient.GetKubernetesVersionByApplication(ctx, app)
	if err != nil {
		rootLogger.Error().Caller().Err(err).Msg("Error retrieving the Kubernetes version")
		k8sVersion = w.ctr.Config.FallbackK8sVersion
	} else {
		k8sVersion = fmt.Sprintf("%s.0", k8sVersion)
		rootLogger.Info().Msgf("Kubernetes version: %s", k8sVersion)
	}

	runner := newRunner(w.ctr, app, appName, k8sVersion, jsonManifests, yamlManifests, rootLogger, w.vcsNote, w.queueApp, w.removeApp)

	// Launch AI review in parallel — posts its own separate comment
	var aiReviewWg sync.WaitGroup
	if w.aiReviewChecker != nil {
		aiReviewWg.Add(1)
		go func() {
			defer aiReviewWg.Done()
			w.runAIReview(ctx, app, appName, k8sVersion, jsonManifests, yamlManifests, rootLogger)
		}()
	}

	for _, processor := range w.processors {
		runner.Run(ctx, processor.Name, processor.Processor, processor.WorstState)
	}

	runner.Wait()
	aiReviewWg.Wait()
}

// runAIReview runs the AI review for a single app and collects the result for aggregation.
func (w *worker) runAIReview(ctx context.Context, app v1alpha1.Application, appName, k8sVersion string, jsonManifests, yamlManifests []string, logger zerolog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error().Caller().Any("error", r).Str("app", appName).Msg("panic in AI review")
		}
	}()

	logger.Info().Str("app", appName).Msg("starting AI review")

	// Get the cloned repo so the AI review can read Chart.yaml for dependencies
	repo, err := w.getRepo(ctx, w.pullRequest.CloneURL, w.pullRequest.HeadRef)
	if err != nil {
		logger.Warn().Caller().Err(err).Msg("failed to get repo for AI review, continuing without chart introspection")
	}

	request := checks.Request{
		App:               app,
		AppName:           appName,
		Container:         w.ctr,
		Repo:              repo,
		JsonManifests:     jsonManifests,
		KubernetesVersion: k8sVersion,
		Log:               logger,
		Note:              w.vcsNote,
		YamlManifests:     yamlManifests,
	}

	result, err := w.aiReviewChecker.Check(ctx, request)
	if err != nil {
		logger.Error().Caller().Err(err).Str("app", appName).Msg("AI review failed")
		w.addAIReviewResult(appName, msg.Result{
			State:   pkg.StateNone,
			Summary: "AI review failed",
			Details: fmt.Sprintf(":warning: AI review for `%s` encountered an error. Try again by commenting `%s`.", appName, w.ctr.Config.ReplanCommentMessage),
		})
		return
	}

	if result.Details == "" {
		logger.Debug().Caller().Str("app", appName).Msg("AI review returned empty result, skipping")
		return
	}

	w.addAIReviewResult(appName, result)
	logger.Info().Str("app", appName).Str("state", result.State.BareString()).Msg("AI review completed")
}

func convertJsonToYamlManifests(jsonManifests []string) []string {
	var manifests []string
	for _, manifest := range jsonManifests {
		ret, err := yaml.JSONToYAML([]byte(manifest))
		if err != nil {
			log.Warn().Err(err).Msg("Failed to format manifest")
			continue
		}
		manifests = append(manifests, fmt.Sprintf("---\n%s", string(ret)))
	}
	return manifests
}
