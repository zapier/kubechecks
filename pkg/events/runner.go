package events

import (
	"context"
	"fmt"
	"sync"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

type Runner struct {
	checks.Request

	wg sync.WaitGroup
}

func newRunner(
	ctr container.Container,
	app v1alpha1.Application,
	appName, k8sVersion string,
	jsonManifests, yamlManifests []string,
	logger zerolog.Logger,
	note *msg.Message,
	queueApp, removeApp func(application v1alpha1.Application),
) *Runner {
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
			YamlManifests:     yamlManifests,
		},
	}
}

type checkFunction func(ctx context.Context, data checks.Request) (msg.Result, error)

func (r *Runner) Run(ctx context.Context, desc string, fn checkFunction, worstState pkg.CommitState) {
	r.wg.Add(1)

	go func() {
		logger := r.Log.With().Str("check", desc).Logger()

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

		logger.Info().Msgf("running check")
		result, err := fn(ctx, r.Request)
		logger.Info().
			Err(err).
			Str("result", result.State.BareString()).
			Msg("check result")

		if err != nil {
			telemetry.SetError(span, err, desc)
			result = msg.Result{State: pkg.StateError, Summary: desc, Details: fmt.Sprintf(errorCommentFormat, desc, err)}
			addToAppMessage(result)
			return
		}

		addToAppMessage(result)
	}()
}

func (r *Runner) Wait() {
	r.wg.Wait()
}
