package events

import (
	"context"
	"fmt"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

type worker struct {
	appChannel chan *v1alpha1.Application
	ctr        container.Container
	logger     zerolog.Logger
	processors []checks.ProcessorEntry
	vcsNote    *msg.Message

	done                func()
	getRepo             func(ctx context.Context, vcsClient hasUsername, cloneURL, branchName string) (*git.Repo, error)
	queueApp, removeApp func(application v1alpha1.Application)
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

		appName    = app.Name
		appSrc     = app.Spec.Source
		appPath    = appSrc.Path
		appRepoUrl = appSrc.RepoURL

		logger = w.logger.With().
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

	logger.Info().Msg("Processing app")

	// Build a new section for this app in the parent comment
	w.vcsNote.AddNewApp(ctx, appName)

	defer func() {
		if err := recover(); err != nil {
			desc := fmt.Sprintf("panic while checking %s", appName)
			w.logger.Error().Str("app", appName).Msgf("panic while running check")

			telemetry.SetError(span, fmt.Errorf("%v", err), "panic while running check")
			result := msg.Result{
				State:   pkg.StatePanic,
				Summary: desc,
				Details: fmt.Sprintf(errorCommentFormat, desc, err),
			}
			w.vcsNote.AddToAppMessage(ctx, appName, result)
		}
	}()

	repo, err := w.getRepo(ctx, w.ctr.VcsClient, appRepoUrl, appSrc.TargetRevision)
	if err != nil {
		logger.Error().Err(err).Msg("Unable to clone repository")
		w.vcsNote.AddToAppMessage(ctx, appName, msg.Result{
			State:   pkg.StateError,
			Summary: "failed to clone repo",
			Details: fmt.Sprintf("Clone URL: `%s`\nTarget Revision: `%s`\n```\n%s\n```", appRepoUrl, appSrc.TargetRevision, err.Error()),
		})
		return
	}
	repoPath := repo.Directory
	var jsonManifests []string

	if !w.ctr.Config.ServerSideDiff {
		logger.Debug().Str("repo_path", repoPath).Msg("Getting manifests")
		jsonManifests, err = w.ctr.ArgoClient.GetManifestsLocal(ctx, appName, repoPath, appPath, app)
		if err != nil {
			logger.Error().Err(err).Msg("Unable to get manifests")
			w.vcsNote.AddToAppMessage(ctx, appName, msg.Result{
				State:   pkg.StateError,
				Summary: "Unable to get manifests",
				Details: fmt.Sprintf("```\n%s\n```", cleanupGetManifestsError(err, repo.Directory)),
			})
			return
		}
	} else {
		logger.Debug().Str("repo_path", repoPath).Msg("Getting server-side manifests")
		jsonManifests, err = w.ctr.ArgoClient.GetManifestsServerSide(ctx, appName, repoPath, appPath, app)
		if err != nil {
			logger.Error().Err(err).Msg("Unable to get manifests")
			w.vcsNote.AddToAppMessage(ctx, appName, msg.Result{
				State:   pkg.StateError,
				Summary: "Unable to get manifests",
				Details: fmt.Sprintf("```\n%s\n```", cleanupGetManifestsError(err, repo.Directory)),
			})
			return
		}

	}

	// Argo diff logic wants unformatted manifests but everything else wants them as YAML, so we prepare both
	yamlManifests := argo_client.ConvertJsonToYamlManifests(jsonManifests)
	logger.Trace().Msgf("Manifests:\n%+v\n", yamlManifests)

	k8sVersion, err := w.ctr.ArgoClient.GetKubernetesVersionByApplication(ctx, app)
	if err != nil {
		logger.Error().Err(err).Msg("Error retrieving the Kubernetes version")
		k8sVersion = w.ctr.Config.FallbackK8sVersion
	} else {
		k8sVersion = fmt.Sprintf("%s.0", k8sVersion)
		logger.Info().Msgf("Kubernetes version: %s", k8sVersion)
	}

	runner := newRunner(
		w.ctr, app, repo, appName, k8sVersion, jsonManifests, yamlManifests, logger, w.vcsNote,
		w.queueApp, w.removeApp,
	)

	for _, processor := range w.processors {
		runner.Run(ctx, processor.Name, processor.Processor, processor.WorstState)
	}

	runner.Wait()
}
