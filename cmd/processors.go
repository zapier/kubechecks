package cmd

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/aiproviders"
	"github.com/zapier/kubechecks/pkg/aiproviders/anthropic"
	"github.com/zapier/kubechecks/pkg/aiproviders/openai"
	"github.com/zapier/kubechecks/pkg/checks"
	aireviewcheck "github.com/zapier/kubechecks/pkg/checks/aireview"
	"github.com/zapier/kubechecks/pkg/checks/diff"
	"github.com/zapier/kubechecks/pkg/checks/hooks"
	"github.com/zapier/kubechecks/pkg/checks/kubeconform"
	"github.com/zapier/kubechecks/pkg/checks/preupgrade"
	"github.com/zapier/kubechecks/pkg/checks/rego"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/helmchart"
)

func getProcessors(ctr container.Container) ([]checks.ProcessorEntry, error) {
	var procs []checks.ProcessorEntry

	procs = append(procs, checks.ProcessorEntry{
		Name:      "generating diff for app",
		Processor: diff.Check,
	})

	if ctr.Config.EnableHooksRenderer {
		procs = append(procs, checks.ProcessorEntry{
			Name:       "render hooks",
			Processor:  hooks.Check,
			WorstState: ctr.Config.WorstHooksState,
		})
	}

	if ctr.Config.EnableKubeConform {
		procs = append(procs, checks.ProcessorEntry{
			Name:       "validating app against schema",
			Processor:  kubeconform.Check,
			WorstState: ctr.Config.WorstKubeConformState,
		})
	}

	if ctr.Config.EnablePreupgrade {
		procs = append(procs, checks.ProcessorEntry{
			Name:       "running pre-upgrade check",
			Processor:  preupgrade.Check,
			WorstState: ctr.Config.WorstPreupgradeState,
		})
	}

	if ctr.Config.EnableConfTest {
		checker, err := rego.NewChecker(ctr.Config)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create rego checker")
		}

		procs = append(procs, checks.ProcessorEntry{
			Name:       "validation policy",
			Processor:  checker.Check,
			WorstState: ctr.Config.WorstConfTestState,
		})
	}

	return procs, nil
}

// getAIReviewChecker creates the AI review checker if enabled, or returns nil.
// The AI review runs separately from the processor pipeline and posts its own comment.
func getAIReviewChecker(ctr container.Container) *aireviewcheck.Checker {
	if !ctr.Config.EnableAIReview {
		return nil
	}

	provider, err := newAIProvider(ctr.Config)
	if err != nil {
		log.Error().Err(err).Msg("failed to create AI review provider, AI review disabled")
		return nil
	}

	checkerOpts := []aireviewcheck.NewCheckerOption{
		aireviewcheck.WithMaxTurns(ctr.Config.AIReviewMaxTurns),
		aireviewcheck.WithTimeout(ctr.Config.AIReviewTimeout),
	}
	if ctr.Config.AIReviewSystemPrompt != "" {
		checkerOpts = append(checkerOpts, aireviewcheck.WithSystemPrompt(ctr.Config.AIReviewSystemPrompt))
	}
	if ctr.Config.AIReviewExtraInstructions != "" {
		checkerOpts = append(checkerOpts, aireviewcheck.WithExtraInstructions(ctr.Config.AIReviewExtraInstructions))
	}
	if ctr.Config.PrometheusURL != "" {
		checkerOpts = append(checkerOpts, aireviewcheck.WithPrometheusURL(ctr.Config.PrometheusURL))
	}
	if ctr.Config.ChartCacheDir != "" {
		chartCache, err := helmchart.NewCache(ctr.Config.ChartCacheDir)
		if err != nil {
			log.Warn().Err(err).Msg("failed to create chart cache, chart introspection disabled")
		} else {
			checkerOpts = append(checkerOpts, aireviewcheck.WithChartCache(chartCache))
		}
	}
	log.Info().
		Str("provider", ctr.Config.AIReviewProvider).
		Str("model", ctr.Config.AIReviewModel).
		Msg("AI review enabled")

	return aireviewcheck.New(
		&aireviewcheck.NewCheckerConfig{Provider: provider},
		checkerOpts...,
	)
}

func newAIProvider(cfg config.ServerConfig) (aiproviders.Provider, error) {
	switch cfg.AIReviewProvider {
	case "anthropic":
		return anthropic.New(cfg.AnthropicAPIKey, cfg.AIReviewModel)
	case "openai":
		return openai.New(cfg.OpenAIAPIToken, cfg.AIReviewModel)
	default:
		return nil, fmt.Errorf("unsupported AI review provider: %q", cfg.AIReviewProvider)
	}
}
