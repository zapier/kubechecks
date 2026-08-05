package aireview

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg/aiproviders"
)

var tracer = otel.Tracer("pkg/aireview")

// Agent orchestrates the agentic tool use loop.
type Agent struct {
	provider        aiproviders.Provider
	model           string
	maxTurns        int
	timeout         time.Duration
	maxOutputTokens int
	temperature     float64
}

// AgentOption configures an Agent.
type AgentOption func(*Agent)

func WithModel(model string) AgentOption {
	return func(a *Agent) { a.model = model }
}

func WithMaxTurns(n int) AgentOption {
	return func(a *Agent) { a.maxTurns = n }
}

func WithTimeout(d time.Duration) AgentOption {
	return func(a *Agent) { a.timeout = d }
}

func WithMaxOutputTokens(n int) AgentOption {
	return func(a *Agent) { a.maxOutputTokens = n }
}

func WithTemperature(t float64) AgentOption {
	return func(a *Agent) { a.temperature = t }
}

// NewAgent creates a new agentic review agent.
func NewAgent(provider aiproviders.Provider, opts ...AgentOption) *Agent {
	a := &Agent{
		provider:        provider,
		maxTurns:        20,
		timeout:         5 * time.Minute,
		maxOutputTokens: 8192,
		temperature:     0.2,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Run executes the agentic loop: sends messages, executes tool calls, repeats until done.
// eventID is used for log correlation (e.g., MR/PR ID + app name).
func (a *Agent) Run(ctx context.Context, eventID string, systemPrompt string, userPrompt string, tools []Tool) (string, error) {
	ctx, span := tracer.Start(ctx, "Agent.Run")
	defer span.End()

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// Build tool defs and executor map
	toolDefs := make([]aiproviders.ToolDef, len(tools))
	toolMap := make(map[string]func(context.Context, json.RawMessage) (string, error), len(tools))
	for i, t := range tools {
		toolDefs[i] = t.Def
		toolMap[t.Def.Name] = t.Execute
	}

	toolCallCount := 0

	messages := []aiproviders.Message{
		{Role: aiproviders.RoleUser, Text: userPrompt},
	}

	for turn := 0; turn < a.maxTurns; turn++ {
		log.Debug().Caller().Str("event_id", eventID).Int("turn", turn).Int("total_tool_calls", toolCallCount).Msg("aireview agent turn")

		resp, err := a.provider.Chat(ctx, aiproviders.ChatRequest{
			Model:        a.model,
			SystemPrompt: systemPrompt,
			Messages:     messages,
			Tools:        toolDefs,
			MaxTokens:    a.maxOutputTokens,
			Temperature:  a.temperature,
		})
		if err != nil {
			return "", fmt.Errorf("aireview turn %d: %w", turn, err)
		}

		// If the model is done, return the text
		if resp.StopReason == aiproviders.StopReasonEndTurn {
			return resp.Text, nil
		}
		if resp.StopReason == aiproviders.StopReasonMaxTokens {
			log.Warn().Str("event_id", eventID).Int("turn", turn).Msg("AI review output truncated due to max output token limit")
			return resp.Text + "\n\n> **Note:** This review was truncated due to output length limits.", nil
		}

		// Log the LLM's reasoning for calling tools
		if resp.Text != "" {
			log.Debug().Caller().Str("event_id", eventID).Int("turn", turn).Str("reasoning", resp.Text).Msg("LLM reasoning before tool calls")
		}

		// Append assistant message with tool calls
		messages = append(messages, aiproviders.Message{
			Role:      aiproviders.RoleAssistant,
			Text:      resp.Text,
			ToolCalls: resp.ToolCalls,
		})

		// Execute tool calls in parallel
		results := make([]aiproviders.ToolResult, len(resp.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range resp.ToolCalls {
			toolCallCount++
			callNum := toolCallCount

			executor, ok := toolMap[tc.Name]
			if !ok {
				log.Warn().Caller().Str("event_id", eventID).Int("tool_call", callNum).Str("tool", tc.Name).Msg("unknown tool called")
				results[i] = aiproviders.ToolResult{
					ToolCallID: tc.ID,
					Content:    fmt.Sprintf("error: unknown tool %q", tc.Name),
					IsError:    true,
				}
				continue
			}

			log.Debug().Caller().Str("event_id", eventID).Int("turn", turn).Int("tool_call", callNum).Int("batch_index", i).Str("tool", tc.Name).Msg("executing tool")
			wg.Add(1)
			go func(idx int, tc aiproviders.ToolCall, callNum int) {
				defer wg.Done()
				output, execErr := executor(ctx, tc.Arguments)
				if execErr != nil {
					log.Warn().Caller().Str("event_id", eventID).Int("tool_call", callNum).Err(execErr).Str("tool", tc.Name).Msg("tool execution failed")
					results[idx] = aiproviders.ToolResult{
						ToolCallID: tc.ID,
						Content:    fmt.Sprintf("error: %s", execErr.Error()),
						IsError:    true,
					}
				} else {
					results[idx] = aiproviders.ToolResult{
						ToolCallID: tc.ID,
						Content:    output,
					}
				}
			}(i, tc, callNum)
		}
		wg.Wait()

		// Append tool results
		messages = append(messages, aiproviders.Message{
			Role:        aiproviders.RoleUser,
			ToolResults: results,
		})
	}

	return "", fmt.Errorf("aireview agent exceeded max turns (%d)", a.maxTurns)
}
