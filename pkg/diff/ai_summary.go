package diff

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/aisummary"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
	"github.com/zapier/kubechecks/telemetry"
	"go.opentelemetry.io/otel"
	"golang.org/x/net/context"
)

const diffAISummaryCommentFormat = `
<details><summary><b>Show AI Summary Diff</b></summary>

%s

</details>
`

func AIDiffSummary(ctx context.Context, mrNote *vcs_clients.Message, name string, manifests []string, diff string) {
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
		mrNote.AddToAppMessage(ctx, name, fmt.Sprintf("failed to summarize diff: %s", err))
		return
	}
	mrNote.AddToAppMessage(ctx, name, fmt.Sprintf(diffAISummaryCommentFormat, aiSummary))
}
