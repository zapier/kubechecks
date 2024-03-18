package aisummary

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/aisummary")

// SummarizeDiff uses ChatGPT to summarize changes to a Kubernetes application.
func (c *OpenAiClient) SummarizeDiff(ctx context.Context, appName string, manifests []string, diff string) (string, error) {
	ctx, span := tracer.Start(ctx, "SummarizeDiff")
	defer span.End()

	model := openai.GPT4Turbo0125
	if len(diff) < 3500 {
		model = openai.GPT3Dot5Turbo
	}

	if c.enabled {
		req := createCompletionRequest(
			model,
			appName,
			summarizeManifestDiffPrompt,
			diff,
			"\n**AI Summary**\n",
		)

		resp, err := c.makeCompletionRequestWithBackoff(ctx, req)
		if err != nil {
			telemetry.SetError(span, err, "ChatCompletionStream error")
			fmt.Printf("ChatCompletionStream error: %v\n", err)
			return "", err
		}

		return resp.Choices[0].Message.Content, nil

	}
	return "", nil
}

const completionSystemPrompt = `You are a helpful Kubernetes expert.
You can summarize Kubernetes YAML manifests for application developers that may not be familiar with all Kubernetes resource types.
Answer as concisely as possible.`

const summarizeManifestDiffPrompt = `Provide a concise summary of the diff (surrounded by the chars "#***") 
that will be applied to the Kubernetes YAML manifests for an application named: %s
Use natural language, bullet points, emoji and format as Gitlab flavored markdown.
Describe the impact of each change.

#***
%s
#***
`
