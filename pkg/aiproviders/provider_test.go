package aiproviders

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolDef_JSONSchema(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "PromQL expression"},
			"duration": {"type": "string", "description": "Range like 7d"}
		},
		"required": ["query"]
	}`)

	td := ToolDef{
		Name:        "query_prometheus",
		Description: "Execute a PromQL query",
		Parameters:  schema,
	}

	assert.Equal(t, "query_prometheus", td.Name)
	assert.Contains(t, string(td.Parameters), "PromQL")
}

func TestMessage_Roles(t *testing.T) {
	assert.Equal(t, Role("user"), RoleUser)
	assert.Equal(t, Role("assistant"), RoleAssistant)
}

func TestStopReason_Values(t *testing.T) {
	assert.Equal(t, StopReason(0), StopReasonEndTurn)
	assert.Equal(t, StopReason(1), StopReasonToolUse)
	assert.Equal(t, StopReason(2), StopReasonMaxTokens)
}
