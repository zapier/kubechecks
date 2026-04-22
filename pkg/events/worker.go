package events

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/ghodss/yaml"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/checks/diff"
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
	addAIReviewResult   func(appName string, result msg.Result, suggestions []vcs.ReviewSuggestion)
	claimAIReviewSlot   func() bool
	changedFiles        []string
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
	jsonManifests, err := w.getManifestsWithRetry(ctx, appName, app, rootLogger)
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

	// Launch AI review in parallel — but only if there are actual changes
	var aiReviewWg sync.WaitGroup
	if w.ctr.Config.EnableAIReview && w.aiReviewChecker != nil {
		// Pre-compute diff to check if there are changes before launching AI review
		diffText, diffErr := diff.GenerateDiffText(ctx, checks.Request{
			App:           app,
			AppName:       appName,
			Container:     w.ctr,
			JsonManifests: jsonManifests,
		})
		if diffErr != nil {
			rootLogger.Warn().Caller().Err(diffErr).Msg("failed to pre-compute diff for AI review skip check, running AI review anyway")
			diffText = "unknown" // run AI review if we can't determine
		}

		if strings.TrimSpace(diffText) == "" {
			rootLogger.Debug().Caller().Str("app", appName).Msg("no manifest changes detected, skipping AI review")
		} else if !w.claimAIReviewSlot() {
			rootLogger.Info().Str("app", appName).Msg("AI review cap reached, skipping AI review for this app")
		} else {
			aiReviewWg.Add(1)
			go func() {
				defer aiReviewWg.Done()
				w.runAIReview(ctx, app, appName, k8sVersion, jsonManifests, yamlManifests, diffText, rootLogger)
			}()
		}
	}

	for _, processor := range w.processors {
		runner.Run(ctx, processor.Name, processor.Processor, processor.WorstState)
	}

	runner.Wait()
	aiReviewWg.Wait()
}

// runAIReview runs the AI review for a single app and collects the result for aggregation.
func (w *worker) runAIReview(ctx context.Context, app v1alpha1.Application, appName, k8sVersion string, jsonManifests, yamlManifests []string, renderedDiff string, logger zerolog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error().Caller().Any("error", r).Str("stack", string(debug.Stack())).Str("app", appName).Msg("panic in AI review")
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
		ChangedFiles:      w.changedFiles,
		RenderedDiff:      renderedDiff,
	}

	result, err := w.aiReviewChecker.Check(ctx, request)
	if err != nil {
		logger.Error().Caller().Err(err).Str("app", appName).Msg("AI review failed")
		w.addAIReviewResult(appName, msg.Result{
			State:   pkg.StateNone,
			Summary: "AI review failed",
			Details: fmt.Sprintf(":warning: AI review for `%s` encountered an error. Try again by commenting `%s`.", appName, w.ctr.Config.ReplanCommentMessage),
		}, nil)
		return
	}

	if result.Result.Details == "" {
		logger.Debug().Caller().Str("app", appName).Msg("AI review returned empty result, skipping")
		return
	}

	w.addAIReviewResult(appName, result.Result, result.Suggestions)
	logger.Info().Str("app", appName).Str("state", result.Result.State.BareString()).Msg("AI review completed")
}

// networkErrorPatterns are substrings in error messages that indicate a transient network error.
// These are matched against the gRPC error description when the gRPC code alone is insufficient
// (ArgoCD wraps both network and Helm errors as codes.Unknown).
var networkErrorPatterns = []string{
	"connection reset by peer",
	"connection refused",
	"no such host",
	"TLS handshake timeout",
	"i/o timeout",
	"dial tcp",
	"server misbehaving",
	"temporary failure in name resolution",
}

const maxManifestRetries = 3

// getManifestsWithRetry calls GetManifests and retries on transient errors.
// Retries on:
//   - gRPC Unavailable (code 14) — always transient
//   - gRPC DeadlineExceeded (code 4) — timeout
//   - gRPC ResourceExhausted (code 8) — rate limiting
//   - gRPC Unknown (code 2) with network error patterns in the description
//
// Does NOT retry:
//   - gRPC Unknown with Helm rendering errors (bad values, schema failures, template errors)
//   - gRPC InvalidArgument, NotFound, PermissionDenied, etc.
func (w *worker) getManifestsWithRetry(ctx context.Context, appName string, app v1alpha1.Application, logger zerolog.Logger) ([]string, error) {
	var lastErr error
	for attempt := 1; attempt <= maxManifestRetries; attempt++ {
		manifests, err := w.ctr.ArgoClient.GetManifests(ctx, appName, app, w.pullRequest, w.getRepo)
		if err == nil {
			return manifests, nil
		}

		lastErr = err
		if !isRetryableError(err) {
			return nil, err
		}

		logger.Warn().Caller().
			Err(err).
			Int("attempt", attempt).
			Int("max_retries", maxManifestRetries).
			Str("app", appName).
			Msg("transient error getting manifests, retrying")

		// Simple backoff: 2s, 4s — skip sleep after final attempt
		if attempt < maxManifestRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*2) * time.Second):
			}
		}
	}
	return nil, lastErr
}

// isRetryableError determines if an error from ArgoCD manifest generation is transient and worth retrying.
func isRetryableError(err error) bool {
	// Check gRPC status code first
	st, ok := grpcstatus.FromError(err)
	if ok {
		switch st.Code() {
		case grpccodes.Unavailable:
			// Always retry — server temporarily unavailable
			return true
		case grpccodes.DeadlineExceeded:
			// Timeout — worth retrying
			return true
		case grpccodes.ResourceExhausted:
			// Rate limited — worth retrying with backoff
			return true
		case grpccodes.Unknown:
			// ArgoCD wraps both network and Helm errors as Unknown.
			// Fall through to string matching on the description.
			return isNetworkErrorMessage(st.Message())
		default:
			// InvalidArgument, NotFound, PermissionDenied, etc. — don't retry
			return false
		}
	}

	// Not a gRPC error — fall back to string matching
	return isNetworkErrorMessage(err.Error())
}

// isNetworkErrorMessage checks if an error message contains patterns indicating a transient network issue.
func isNetworkErrorMessage(msg string) bool {
	for _, pattern := range networkErrorPatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
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
