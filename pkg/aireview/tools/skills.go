package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/zapier/kubechecks/pkg/a2askills"
	"github.com/zapier/kubechecks/pkg/aireview"
)

// A2ASkillTool builds an aireview.Tool from an a2a.AgentSkill discovered at
// startup. The tool name, description, and tags come directly from the agent
// card — no hardcoded schema on the kubechecks side. Params are passed as a
// free-form JSON object; the LLM constructs them guided by the description.
//
// Tool failure returns an error string so the review continues uninterrupted.
func A2ASkillTool(client a2askills.Client, skill a2a.AgentSkill, timeout time.Duration) aireview.Tool {
	return aireview.NewTool(
		skill.ID,
		skill.Description,
		genericParamsSchema(skill.Tags),
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params map[string]any
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid params for %s: %w", skill.ID, err)
			}
			callCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			result, err := client.Call(callCtx, skill.ID, params)
			if err != nil {
				// Surface as a tool result so the review continues without the skill.
				return fmt.Sprintf("%s unavailable: %s", skill.ID, err), nil
			}
			return result, nil
		},
	)
}

// genericParamsSchema returns a JSON Schema that accepts any object, with a
// hint listing the skill's tags as suggested param keys. The LLM is free to
// pass any fields — the agent handles unknown params gracefully.
func genericParamsSchema(tags []string) json.RawMessage {
	schema, err := json.Marshal(map[string]any{
		"type":                 "object",
		"description":          fmt.Sprintf("Parameters for this skill. Pass any relevant key-value pairs. Tag hints: %v", tags),
		"additionalProperties": true,
	})
	if err != nil {
		return json.RawMessage(`{"type":"object","additionalProperties":true}`)
	}
	return json.RawMessage(schema)
}
