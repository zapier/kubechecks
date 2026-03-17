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

	if ctr.Config.EnableAIReview {
		provider, err := newAIProvider(ctr.Config)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create AI review provider")
		}

		checkerOpts := []aireviewcheck.NewCheckerOption{
			aireviewcheck.WithMaxTurns(ctr.Config.AIReviewMaxTurns),
			aireviewcheck.WithTimeout(ctr.Config.AIReviewTimeout),
		}
		if ctr.Config.AIReviewSystemPrompt != "" {
			checkerOpts = append(checkerOpts, aireviewcheck.WithSystemPrompt(ctr.Config.AIReviewSystemPrompt))
		}
		if ctr.Config.PrometheusURL != "" {
			checkerOpts = append(checkerOpts, aireviewcheck.WithPrometheusURL(ctr.Config.PrometheusURL))
		}

		checker := aireviewcheck.New(
			&aireviewcheck.NewCheckerConfig{Provider: provider},
			checkerOpts...,
		)
		log.Info().
			Str("provider", ctr.Config.AIReviewProvider).
			Str("model", ctr.Config.AIReviewModel).
			Msg("AI review enabled")

		procs = append(procs, checks.ProcessorEntry{
			Name:       "AI impact review",
			Processor:  checker.Check,
			WorstState: ctr.Config.WorstAIReviewState,
		})
	}

	return procs, nil
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
