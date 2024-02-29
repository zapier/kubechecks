package diff

import (
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"golang.org/x/net/context"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/aisummary"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/diff")

func aiDiffSummary(ctx context.Context, mrNote *msg.Message, cfg config.ServerConfig, name string, manifests []string, diff string) {
	ctx, span := tracer.Start(ctx, "aiDiffSummary")
	defer span.End()

	log.Debug().Str("name", name).Msg("generating ai diff summary for application...")
	if mrNote == nil {
		return
	}

	aiSummary, err := aisummary.GetOpenAiClient(cfg.OpenAIAPIToken).SummarizeDiff(ctx, name, manifests, diff)
	if err != nil {
		telemetry.SetError(span, err, "OpenAI SummarizeDiff")
		log.Error().Err(err).Msg("failed to summarize diff")
		cr := msg.Result{State: pkg.StateNone, Summary: "failed to summarize diff", Details: err.Error()}
		mrNote.AddToAppMessage(ctx, name, cr)
		return
	}

	if aiSummary != "" {
		cr := msg.Result{State: pkg.StateNone, Summary: "<b>Show AI Summary Diff</b>", Details: aiSummary}
		mrNote.AddToAppMessage(ctx, name, cr)
	}
}
