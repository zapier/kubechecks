package openai

import (
	"context"
	"encoding/json"
	"fmt"

	goopenai "github.com/sashabaranov/go-openai"

	"github.com/zapier/kubechecks/pkg/aiproviders"
)

type Provider struct {
	client *goopenai.Client
	model  string
}

func New(apiKey, model string) (*Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openai API key is required")
	}
	return &Provider{
		client: goopenai.NewClient(apiKey),
		model:  model,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req aiproviders.ChatRequest) (*aiproviders.ChatResponse, error) {
	model := p.model
	if req.Model != "" {
		model = req.Model
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	messages := convertMessages(req.SystemPrompt, req.Messages)

	openaiReq := goopenai.ChatCompletionRequest{
		Model:       model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: float32(req.Temperature),
	}

	if len(req.Tools) > 0 {
		openaiReq.Tools = convertTools(req.Tools)
	}

	resp, err := p.client.CreateChatCompletion(ctx, openaiReq)
	if err != nil {
		return nil, fmt.Errorf("openai chat: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai chat: no choices returned")
	}

	return convertResponse(resp.Choices[0]), nil
}

func convertMessages(systemPrompt string, msgs []aiproviders.Message) []goopenai.ChatCompletionMessage {
	var out []goopenai.ChatCompletionMessage

	// System prompt is a regular message in OpenAI
	if systemPrompt != "" {
		out = append(out, goopenai.ChatCompletionMessage{
			Role:    goopenai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}

	for _, m := range msgs {
		switch m.Role {
		case aiproviders.RoleAssistant:
			msg := goopenai.ChatCompletionMessage{
				Role:    goopenai.ChatMessageRoleAssistant,
				Content: m.Text,
			}
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, goopenai.ToolCall{
					ID:   tc.ID,
					Type: goopenai.ToolTypeFunction,
					Function: goopenai.FunctionCall{
						Name:      tc.Name,
						Arguments: string(tc.Arguments),
					},
				})
			}
			out = append(out, msg)

		case aiproviders.RoleUser:
			if len(m.ToolResults) > 0 {
				// Each tool result is a separate message in OpenAI
				for _, tr := range m.ToolResults {
					out = append(out, goopenai.ChatCompletionMessage{
						Role:       goopenai.ChatMessageRoleTool,
						Content:    tr.Content,
						ToolCallID: tr.ToolCallID,
					})
				}
			} else {
				out = append(out, goopenai.ChatCompletionMessage{
					Role:    goopenai.ChatMessageRoleUser,
					Content: m.Text,
				})
			}
		}
	}

	return out
}

func convertTools(tools []aiproviders.ToolDef) []goopenai.Tool {
	out := make([]goopenai.Tool, len(tools))
	for i, t := range tools {
		// Parameters is already JSON Schema, pass it through as-is
		var params any
		_ = json.Unmarshal(t.Parameters, &params)

		out[i] = goopenai.Tool{
			Type: goopenai.ToolTypeFunction,
			Function: &goopenai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		}
	}
	return out
}

func convertResponse(choice goopenai.ChatCompletionChoice) *aiproviders.ChatResponse {
	resp := &aiproviders.ChatResponse{
		Text: choice.Message.Content,
	}

	// Map finish reason
	switch choice.FinishReason {
	case goopenai.FinishReasonToolCalls, goopenai.FinishReasonFunctionCall:
		resp.StopReason = aiproviders.StopReasonToolUse
	case goopenai.FinishReasonLength:
		resp.StopReason = aiproviders.StopReasonMaxTokens
	default:
		resp.StopReason = aiproviders.StopReasonEndTurn
	}

	// Extract tool calls
	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, aiproviders.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	return resp
}
