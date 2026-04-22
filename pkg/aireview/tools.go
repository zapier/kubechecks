package aireview

import (
	"context"
	"encoding/json"

	"github.com/zapier/kubechecks/pkg/aiproviders"
)

// Tool binds a tool definition to its executor function.
type Tool struct {
	Def     aiproviders.ToolDef
	Execute func(ctx context.Context, input json.RawMessage) (string, error)
}

// NewTool creates a Tool from a name, description, JSON schema parameters, and executor.
func NewTool(name, description string, schema json.RawMessage, exec func(ctx context.Context, input json.RawMessage) (string, error)) Tool {
	return Tool{
		Def: aiproviders.ToolDef{
			Name:        name,
			Description: description,
			Parameters:  schema,
		},
		Execute: exec,
	}
}
