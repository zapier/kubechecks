package aireview

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/aiproviders"
)

// mockProvider returns a sequence of ChatResponses.
type mockProvider struct {
	responses []aiproviders.ChatResponse
	requests  []aiproviders.ChatRequest
	callCount int
}

func (m *mockProvider) Chat(_ context.Context, req aiproviders.ChatRequest) (*aiproviders.ChatResponse, error) {
	m.requests = append(m.requests, req)
	if m.callCount >= len(m.responses) {
		return nil, fmt.Errorf("unexpected call %d", m.callCount)
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return &resp, nil
}

func TestAgent_Run_NoTools(t *testing.T) {
	provider := &mockProvider{
		responses: []aiproviders.ChatResponse{
			{Text: "The diff looks good.", StopReason: aiproviders.StopReasonEndTurn},
		},
	}

	agent := NewAgent(provider, WithTimeout(10*time.Second))
	result, err := agent.Run(context.Background(), "test-1", "You are a reviewer.", "Review this diff.", nil)

	require.NoError(t, err)
	assert.Equal(t, "The diff looks good.", result)
	assert.Equal(t, 1, provider.callCount)
	assert.Equal(t, "You are a reviewer.", provider.requests[0].SystemPrompt)
}

func TestAgent_Run_WithToolCall(t *testing.T) {
	provider := &mockProvider{
		responses: []aiproviders.ChatResponse{
			{
				StopReason: aiproviders.StopReasonToolUse,
				ToolCalls: []aiproviders.ToolCall{
					{
						ID:        "call_1",
						Name:      "get_diff",
						Arguments: json.RawMessage(`{}`),
					},
				},
			},
			{
				Text:       "Based on the diff, LGTM.",
				StopReason: aiproviders.StopReasonEndTurn,
			},
		},
	}

	getDiff := NewTool("get_diff", "Get the manifest diff", json.RawMessage(`{"type":"object","properties":{}}`),
		func(ctx context.Context, input json.RawMessage) (string, error) {
			return "--- a/deploy.yaml\n+++ b/deploy.yaml\n-replicas: 2\n+replicas: 5", nil
		},
	)

	agent := NewAgent(provider, WithTimeout(10*time.Second))
	result, err := agent.Run(context.Background(), "test-1", "Review.", "Check this.", []Tool{getDiff})

	require.NoError(t, err)
	assert.Equal(t, "Based on the diff, LGTM.", result)
	assert.Equal(t, 2, provider.callCount)

	// Verify tool result was passed back in the second request
	secondReq := provider.requests[1]
	require.Len(t, secondReq.Messages, 3) // user prompt + assistant tool_call + user tool_result
	assert.Equal(t, aiproviders.RoleUser, secondReq.Messages[2].Role)
	require.Len(t, secondReq.Messages[2].ToolResults, 1)
	assert.Equal(t, "call_1", secondReq.Messages[2].ToolResults[0].ToolCallID)
	assert.Contains(t, secondReq.Messages[2].ToolResults[0].Content, "replicas: 5")
}

func TestAgent_Run_ToolError(t *testing.T) {
	provider := &mockProvider{
		responses: []aiproviders.ChatResponse{
			{
				StopReason: aiproviders.StopReasonToolUse,
				ToolCalls: []aiproviders.ToolCall{
					{
						ID:        "call_1",
						Name:      "query_prometheus",
						Arguments: json.RawMessage(`{"query":"up"}`),
					},
				},
			},
			{
				Text:       "Prometheus query failed, but the config looks reasonable.",
				StopReason: aiproviders.StopReasonEndTurn,
			},
		},
	}

	promTool := NewTool("query_prometheus", "Query Prometheus", json.RawMessage(`{"type":"object","properties":{}}`),
		func(ctx context.Context, input json.RawMessage) (string, error) {
			return "", fmt.Errorf("connection refused")
		},
	)

	agent := NewAgent(provider, WithTimeout(10*time.Second))
	result, err := agent.Run(context.Background(), "test-1", "Review.", "Check this.", []Tool{promTool})

	require.NoError(t, err)
	assert.Contains(t, result, "Prometheus query failed")

	// Verify error was flagged
	secondReq := provider.requests[1]
	toolResult := secondReq.Messages[2].ToolResults[0]
	assert.True(t, toolResult.IsError)
	assert.Contains(t, toolResult.Content, "connection refused")
}

func TestAgent_Run_UnknownTool(t *testing.T) {
	provider := &mockProvider{
		responses: []aiproviders.ChatResponse{
			{
				StopReason: aiproviders.StopReasonToolUse,
				ToolCalls: []aiproviders.ToolCall{
					{
						ID:        "call_1",
						Name:      "nonexistent_tool",
						Arguments: json.RawMessage(`{}`),
					},
				},
			},
			{
				Text:       "I'll proceed without that tool.",
				StopReason: aiproviders.StopReasonEndTurn,
			},
		},
	}

	agent := NewAgent(provider, WithTimeout(10*time.Second))
	result, err := agent.Run(context.Background(), "test-1", "Review.", "Check.", nil)

	require.NoError(t, err)
	assert.Contains(t, result, "proceed without")

	// Verify unknown tool error was sent back
	toolResult := provider.requests[1].Messages[2].ToolResults[0]
	assert.True(t, toolResult.IsError)
	assert.Contains(t, toolResult.Content, "unknown tool")
}

func TestAgent_Run_MaxTurnsExceeded(t *testing.T) {
	// Provider always returns tool calls
	provider := &mockProvider{
		responses: make([]aiproviders.ChatResponse, 10),
	}
	for i := range provider.responses {
		provider.responses[i] = aiproviders.ChatResponse{
			StopReason: aiproviders.StopReasonToolUse,
			ToolCalls: []aiproviders.ToolCall{
				{ID: fmt.Sprintf("call_%d", i), Name: "get_diff", Arguments: json.RawMessage(`{}`)},
			},
		}
	}

	getDiff := NewTool("get_diff", "Get diff", json.RawMessage(`{"type":"object","properties":{}}`),
		func(ctx context.Context, input json.RawMessage) (string, error) {
			return "some diff", nil
		},
	)

	agent := NewAgent(provider, WithMaxTurns(3), WithTimeout(10*time.Second))
	_, err := agent.Run(context.Background(), "test-1", "Review.", "Check.", []Tool{getDiff})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max turns (3)")
}

func TestAgent_Run_MultipleToolCalls(t *testing.T) {
	provider := &mockProvider{
		responses: []aiproviders.ChatResponse{
			{
				StopReason: aiproviders.StopReasonToolUse,
				ToolCalls: []aiproviders.ToolCall{
					{ID: "call_1", Name: "get_diff", Arguments: json.RawMessage(`{}`)},
					{ID: "call_2", Name: "get_live", Arguments: json.RawMessage(`{}`)},
				},
			},
			{
				Text:       "Both tools returned data. Analysis complete.",
				StopReason: aiproviders.StopReasonEndTurn,
			},
		},
	}

	getDiff := NewTool("get_diff", "Get diff", json.RawMessage(`{"type":"object","properties":{}}`),
		func(ctx context.Context, input json.RawMessage) (string, error) {
			return "diff content", nil
		},
	)
	getLive := NewTool("get_live", "Get live manifests", json.RawMessage(`{"type":"object","properties":{}}`),
		func(ctx context.Context, input json.RawMessage) (string, error) {
			return "live content", nil
		},
	)

	agent := NewAgent(provider, WithTimeout(10*time.Second))
	result, err := agent.Run(context.Background(), "test-1", "Review.", "Check.", []Tool{getDiff, getLive})

	require.NoError(t, err)
	assert.Contains(t, result, "Analysis complete")

	// Verify both tool results were sent back
	toolResults := provider.requests[1].Messages[2].ToolResults
	require.Len(t, toolResults, 2)
	assert.Equal(t, "call_1", toolResults[0].ToolCallID)
	assert.Equal(t, "call_2", toolResults[1].ToolCallID)
}
