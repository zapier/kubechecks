package openai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/aiproviders"
)

type Provider struct {
	client openai.Client
	model  string
}

func New(apiKey, model string) (*Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openai API key is required")
	}
	client := openai.NewClient(option.WithAPIKey(apiKey))
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

	messages := convertMessages(req.SystemPrompt, req.Messages)

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: messages,
	}

	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}
	if len(req.Tools) > 0 {
		params.Tools = convertTools(req.Tools)
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai chat: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai chat: no choices returned")
	}

	return convertResponse(resp.Choices[0]), nil
}

func convertMessages(systemPrompt string, msgs []aiproviders.Message) []openai.ChatCompletionMessageParamUnion {
	var out []openai.ChatCompletionMessageParamUnion

	if systemPrompt != "" {
		out = append(out, openai.SystemMessage(systemPrompt))
	}

	for _, m := range msgs {
		switch m.Role {
		case aiproviders.RoleAssistant:
			if len(m.ToolCalls) == 0 {
				out = append(out, openai.AssistantMessage(m.Text))
			} else {
				// Build assistant message with tool calls
				asst := openai.ChatCompletionAssistantMessageParam{}
				if m.Text != "" {
					asst.Content.OfString = openai.String(m.Text)
				}
				for _, tc := range m.ToolCalls {
					asst.ToolCalls = append(asst.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: tc.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Name,
								Arguments: string(tc.Arguments),
							},
						},
					})
				}
				out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
			}

		case aiproviders.RoleUser:
			if len(m.ToolResults) > 0 {
				for _, tr := range m.ToolResults {
					out = append(out, openai.ToolMessage(tr.Content, tr.ToolCallID))
				}
			} else {
				out = append(out, openai.UserMessage(m.Text))
			}
		}
	}

	return out
}

func convertTools(tools []aiproviders.ToolDef) []openai.ChatCompletionToolUnionParam {
	out := make([]openai.ChatCompletionToolUnionParam, len(tools))
	for i, t := range tools {
		var params shared.FunctionParameters
		if err := json.Unmarshal(t.Parameters, &params); err != nil {
			log.Warn().Err(err).Str("tool", t.Name).Msg("failed to unmarshal tool parameter schema")
		}

		out[i] = openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  params,
		})
	}
	return out
}

func convertResponse(choice openai.ChatCompletionChoice) *aiproviders.ChatResponse {
	resp := &aiproviders.ChatResponse{
		Text: choice.Message.Content,
	}

	switch choice.FinishReason {
	case "tool_calls", "function_call":
		resp.StopReason = aiproviders.StopReasonToolUse
	case "length":
		resp.StopReason = aiproviders.StopReasonMaxTokens
	default:
		resp.StopReason = aiproviders.StopReasonEndTurn
	}

	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, aiproviders.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	return resp
}
