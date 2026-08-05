package diff

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/aiproviders"
	"github.com/zapier/kubechecks/pkg/aiproviders/anthropic"
	"github.com/zapier/kubechecks/pkg/aiproviders/openai"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/checks/diff")

const diffSummarySystemPrompt = `You are a helpful Kubernetes expert.
You can summarize Kubernetes YAML manifests for application developers that may not be familiar with all Kubernetes resource types.
Answer as concisely as possible.`

const diffSummaryUserPrompt = `Provide a concise summary of the diff (surrounded by the chars "#***")
that will be applied to the Kubernetes YAML manifests for an application named: %s
Use natural language, bullet points, emoji and format as Gitlab flavored markdown.
Describe the impact of each change.

#***
%s
#***
`

func aiDiffSummary(ctx context.Context, mrNote *msg.Message, cfg config.ServerConfig, name, diff string) {
	ctx, span := tracer.Start(ctx, "aiDiffSummary")
	defer span.End()

	log.Debug().Caller().Str("name", name).Msg("generating ai diff summary for application...")
	if mrNote == nil {
		return
	}

	provider, err := newDiffSummaryProvider(cfg)
	if err != nil {
		log.Debug().Caller().Err(err).Msg("AI diff summary provider not configured, skipping")
		return
	}

	resp, err := provider.Chat(ctx, aiproviders.ChatRequest{
		Model:        cfg.AIReviewModel,
		SystemPrompt: diffSummarySystemPrompt,
		Messages: []aiproviders.Message{
			{
				Role: aiproviders.RoleUser,
				Text: fmt.Sprintf(diffSummaryUserPrompt, name, diff),
			},
		},
		MaxTokens:   500,
		Temperature: 0.4,
	})
	if err != nil {
		telemetry.SetError(span, err, "AI SummarizeDiff")
		log.Error().Err(err).Msg("failed to summarize diff")
		cr := msg.Result{State: pkg.StateNone, Summary: "failed to summarize diff", Details: err.Error()}
		mrNote.AddToAppMessage(ctx, name, cr)
		return
	}

	aiSummary := cleanUpAiSummary(resp.Text)
	if aiSummary == "" {
		return
	}

	cr := msg.Result{State: pkg.StateNone, Summary: "<b>Show AI Summary Diff</b>", Details: aiSummary}
	mrNote.AddToAppMessage(ctx, name, cr)
}

// newDiffSummaryProvider creates a provider for diff summarization based on config.
// It uses the same provider/model config as AI review.
func newDiffSummaryProvider(cfg config.ServerConfig) (aiproviders.Provider, error) {
	switch cfg.AIReviewProvider {
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("anthropic API key not configured")
		}
		return anthropic.New(cfg.AnthropicAPIKey, cfg.AIReviewModel)
	case "openai":
		if cfg.OpenAIAPIToken == "" {
			return nil, fmt.Errorf("openai API token not configured")
		}
		return openai.New(cfg.OpenAIAPIToken, cfg.AIReviewModel)
	default:
		// Fallback: if openai token exists, use openai (backwards compat)
		if cfg.OpenAIAPIToken != "" {
			return openai.New(cfg.OpenAIAPIToken, "gpt-4o")
		}
		return nil, fmt.Errorf("no AI provider configured for diff summary")
	}
}

var codeBlockBegin = regexp.MustCompile("^```(\\w+)")

func cleanUpAiSummary(aiSummary string) string {
	aiSummary = strings.TrimSpace(aiSummary)

	// occasionally the model thinks it should wrap it in a code block.
	// comments do not need this, as they are already rendered as markdown.
	for {
		newSummary := aiSummary

		newSummary = codeBlockBegin.ReplaceAllString(newSummary, "")
		newSummary = strings.TrimPrefix(newSummary, "#***")
		newSummary = strings.TrimSuffix(newSummary, "```")
		newSummary = strings.TrimSuffix(newSummary, "#***")
		newSummary = strings.TrimSpace(newSummary)

		if newSummary == aiSummary {
			break
		}

		aiSummary = newSummary
	}

	return strings.TrimSpace(aiSummary)
}
