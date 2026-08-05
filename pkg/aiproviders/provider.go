package aiproviders

import (
	"context"
	"encoding/json"
)

// Provider abstracts the LLM chat completion API with tool use support.
// Both Anthropic and OpenAI implement this interface.
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

type ChatRequest struct {
	Model        string
	SystemPrompt string
	Messages     []Message
	Tools        []ToolDef
	MaxTokens    int
	Temperature  float64
}

type Message struct {
	Role        Role
	Text        string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema object
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ToolResult struct {
	ToolCallID string
	Content    string
	IsError    bool
}

type StopReason int

const (
	StopReasonEndTurn StopReason = iota
	StopReasonToolUse
	StopReasonMaxTokens
)

type ChatResponse struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason StopReason
}
