package events

import (
	"context"
	"fmt"
	"sync"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

type Runner struct {
	checks.Request

	wg sync.WaitGroup
}

func newRunner(
	ctr container.Container, app v1alpha1.Application, repo *git.Repo,
	appName, k8sVersion string, jsonManifests, yamlManifests []string,
	logger zerolog.Logger, note *msg.Message, queueApp, removeApp func(application v1alpha1.Application),
) *Runner {
	logger = logger.
		With().
		Str("app", appName).
		Logger()

	return &Runner{
		Request: checks.Request{
			App:               app,
			AppName:           appName,
			Container:         ctr,
			JsonManifests:     jsonManifests,
			KubernetesVersion: k8sVersion,
			Log:               logger,
			Note:              note,
			QueueApp:          queueApp,
			RemoveApp:         removeApp,
			Repo:              repo,
			YamlManifests:     yamlManifests,
		},
	}
}

type checkFunction func(ctx context.Context, data checks.Request) (msg.Result, error)

func (r *Runner) Run(ctx context.Context, desc string, fn checkFunction, worstState pkg.CommitState) {
	r.wg.Add(1)

	go func() {
		logger := r.Log

		ctx, span := tracer.Start(ctx, desc)

		addToAppMessage := func(result msg.Result) {
			result.State = pkg.BestState(result.State, worstState)
			r.Note.AddToAppMessage(ctx, r.AppName, result)
		}

		defer func() {
			r.wg.Done()

			if err := recover(); err != nil {
				logger.Error().Str("check", desc).Msgf("panic while running check")

				telemetry.SetError(span, fmt.Errorf("%v", err), desc)
				result := msg.Result{
					State:   pkg.StatePanic,
					Summary: desc,
					Details: fmt.Sprintf(errorCommentFormat, desc, err),
				}
				addToAppMessage(result)
			}
		}()

		logger = logger.With().
			Str("check", desc).
			Logger()

		logger.Info().Msgf("running check")
		result, err := fn(ctx, r.Request)
		logger.Info().
			Err(err).
			Uint8("result", uint8(result.State)).
			Msg("check result")

		if err != nil {
			telemetry.SetError(span, err, desc)
			result = msg.Result{State: pkg.StateError, Summary: desc, Details: fmt.Sprintf(errorCommentFormat, desc, err)}
			addToAppMessage(result)
			return
		}

		addToAppMessage(result)

		logger.Info().
			Str("result", result.State.BareString()).
			Msgf("check done")
	}()
}

func (r *Runner) Wait() {
	r.wg.Wait()
}
