package anthropic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/aiproviders"
)

type Provider struct {
	client anthropic.Client
	model  string
}

func New(apiKey, model string) (*Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic API key is required")
	}
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &Provider{
		client: client,
		model:  model,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req aiproviders.ChatRequest) (*aiproviders.ChatResponse, error) {
	model := p.model
	if req.Model != "" {
		model = req.Model
	}

	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		Messages:  convertMessages(req.Messages),
	}

	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{
				Text:         req.SystemPrompt,
				CacheControl: anthropic.NewCacheControlEphemeralParam(),
			},
		}
	}

	if req.Temperature > 0 {
		params.Temperature = anthropic.Float(req.Temperature)
	}

	if len(req.Tools) > 0 {
		params.Tools = convertTools(req.Tools)
	}

	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic chat: %w", err)
	}

	return convertResponse(msg), nil
}

func convertMessages(msgs []aiproviders.Message) []anthropic.MessageParam {
	var out []anthropic.MessageParam
	for _, m := range msgs {
		switch m.Role {
		case aiproviders.RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion
			if m.Text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Text))
			}
			for _, tc := range m.ToolCalls {
				// Re-serialize arguments to any for the SDK
				var input any
				if err := json.Unmarshal(tc.Arguments, &input); err != nil {
					log.Warn().Err(err).Str("tool", tc.Name).Msg("failed to unmarshal tool call arguments")
					input = map[string]any{}
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
			}
			out = append(out, anthropic.NewAssistantMessage(blocks...))

		case aiproviders.RoleUser:
			if len(m.ToolResults) > 0 {
				// Tool results are sent as content blocks in a user message
				var blocks []anthropic.ContentBlockParamUnion
				for _, tr := range m.ToolResults {
					blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, tr.Content, tr.IsError))
				}
				out = append(out, anthropic.NewUserMessage(blocks...))
			} else {
				// Cache user message (the review context that stays constant across tool-use rounds)
				block := anthropic.NewTextBlock(m.Text)
				*block.GetCacheControl() = anthropic.NewCacheControlEphemeralParam()
				out = append(out, anthropic.NewUserMessage(block))
			}
		}
	}

	return out
}

func convertTools(tools []aiproviders.ToolDef) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, len(tools))
	for i, t := range tools {
		// Parse the JSON Schema to extract properties and required fields
		var schema struct {
			Properties any      `json:"properties"`
			Required   []string `json:"required"`
		}
		if err := json.Unmarshal(t.Parameters, &schema); err != nil {
			log.Warn().Err(err).Str("tool", t.Name).Msg("failed to unmarshal tool parameter schema")
		}

		out[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: schema.Properties,
					Required:   schema.Required,
				},
			},
		}
	}
	return out
}

func convertResponse(msg *anthropic.Message) *aiproviders.ChatResponse {
	resp := &aiproviders.ChatResponse{}

	// Map stop reason
	switch msg.StopReason {
	case anthropic.StopReasonToolUse:
		resp.StopReason = aiproviders.StopReasonToolUse
	case anthropic.StopReasonMaxTokens:
		resp.StopReason = aiproviders.StopReasonMaxTokens
	default:
		resp.StopReason = aiproviders.StopReasonEndTurn
	}

	// Extract text and tool calls from content blocks
	for _, block := range msg.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			resp.Text += v.Text
		case anthropic.ToolUseBlock:
			resp.ToolCalls = append(resp.ToolCalls, aiproviders.ToolCall{
				ID:        v.ID,
				Name:      v.Name,
				Arguments: v.Input,
			})
		}
	}

	return resp
}
