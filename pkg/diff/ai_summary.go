package diff

import (
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"golang.org/x/net/context"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/aisummary"
	"github.com/zapier/kubechecks/telemetry"
)

func AIDiffSummary(ctx context.Context, mrNote *pkg.Message, name string, manifests []string, diff string) {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "AIDiffSummary")
	defer span.End()

	log.Debug().Str("name", name).Msg("generating ai diff summary for application...")
	if mrNote == nil {
		return
	}

	aiSummary, err := aisummary.GetOpenAiClient().SummarizeDiff(ctx, name, manifests, diff)
	if err != nil {
		telemetry.SetError(span, err, "OpenAI SummarizeDiff")
		log.Error().Err(err).Msg("failed to summarize diff")
		cr := pkg.CheckResult{State: pkg.StateWarning, Summary: "failed to summarize diff", Details: err.Error()}
		mrNote.AddToAppMessage(ctx, name, cr)
		return
	}

	if aiSummary != "" {
		cr := pkg.CheckResult{State: pkg.StateNone, Summary: "<b>Show AI Summary Diff</b>", Details: aiSummary}
		mrNote.AddToAppMessage(ctx, name, cr)
	}
}
