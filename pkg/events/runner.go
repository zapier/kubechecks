package events

import (
	"context"
	"fmt"
	"sync"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

type CheckData struct {
	span   trace.Span
	ctx    context.Context
	logger zerolog.Logger
	note   *msg.Message
	app    v1alpha1.Application

	appName       string
	k8sVersion    string
	repoPath      string
	jsonManifests []string
	yamlManifests []string
}

type Runner struct {
	CheckData

	wg sync.WaitGroup
}

func newRunner(
	span trace.Span, ctx context.Context, app v1alpha1.Application,
	appName, k8sVersion, repoPath string, jsonManifests, yamlManifests []string,
	logger zerolog.Logger, note *msg.Message,
) *Runner {
	logger = logger.
		With().
		Str("app", appName).
		Logger()

	return &Runner{
		CheckData: CheckData{
			app:           app,
			appName:       appName,
			k8sVersion:    k8sVersion,
			repoPath:      repoPath,
			jsonManifests: jsonManifests,
			yamlManifests: yamlManifests,

			ctx:    ctx,
			logger: logger,
			note:   note,
			span:   span,
		},
	}
}

type checkFunction func(data CheckData) (msg.CheckResult, error)

func (r *Runner) Run(desc string, fn checkFunction) {
	r.wg.Add(1)

	go func() {
		logger := r.logger

		defer func() {
			r.wg.Done()

			if err := recover(); err != nil {
				logger.Error().Str("check", desc).Msgf("panic while running check")

				telemetry.SetError(r.span, fmt.Errorf("%v", err), desc)
				result := msg.CheckResult{
					State:   pkg.StatePanic,
					Summary: desc,
					Details: fmt.Sprintf(errorCommentFormat, desc, err),
				}
				r.note.AddToAppMessage(r.ctx, r.appName, result)
			}
		}()

		logger = logger.With().
			Str("check", desc).
			Logger()

		logger.Info().Msgf("running check")
		cr, err := fn(r.CheckData)
		logger.Info().
			Err(err).
			Uint8("result", uint8(cr.State)).
			Msg("check result")

		if err != nil {
			telemetry.SetError(r.span, err, desc)
			result := msg.CheckResult{State: pkg.StateError, Summary: desc, Details: fmt.Sprintf(errorCommentFormat, desc, err)}
			r.note.AddToAppMessage(r.ctx, r.appName, result)
			return
		}

		r.note.AddToAppMessage(r.ctx, r.appName, cr)

		logger.Info().
			Str("result", cr.State.BareString()).
			Msgf("check done")
	}()
}

func (r *Runner) Wait() {
	r.wg.Wait()
}
