package diff

import (
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"golang.org/x/net/context"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/aisummary"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/checks/diff")

func aiDiffSummary(ctx context.Context, mrNote *msg.Message, cfg config.ServerConfig, name, diff string) {
	ctx, span := tracer.Start(ctx, "aiDiffSummary")
	defer span.End()

	log.Debug().Str("name", name).Msg("generating ai diff summary for application...")
	if mrNote == nil {
		return
	}

	aiClient := aisummary.GetOpenAiClient(cfg.OpenAIAPIToken)
	aiSummary, err := aiClient.SummarizeDiff(ctx, name, diff)
	if err != nil {
		telemetry.SetError(span, err, "OpenAI SummarizeDiff")
		log.Error().Err(err).Msg("failed to summarize diff")
		cr := msg.Result{State: pkg.StateNone, Summary: "failed to summarize diff", Details: err.Error()}
		mrNote.AddToAppMessage(ctx, name, cr)
		return
	}

	aiSummary = cleanUpAiSummary(aiSummary)
	if aiSummary == "" {
		return
	}

	cr := msg.Result{State: pkg.StateNone, Summary: "<b>Show AI Summary Diff</b>", Details: aiSummary}
	mrNote.AddToAppMessage(ctx, name, cr)
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
