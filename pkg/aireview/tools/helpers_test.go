package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanObject(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name:     "nil metadata",
			input:    map[string]any{"kind": "Deployment"},
			expected: map[string]any{"kind": "Deployment"},
		},
		{
			name: "removes managedFields",
			input: map[string]any{
				"metadata": map[string]any{
					"name":          "web",
					"managedFields": []any{"field1", "field2"},
				},
			},
			expected: map[string]any{
				"metadata": map[string]any{
					"name": "web",
				},
			},
		},
		{
			name: "removes last-applied-configuration annotation",
			input: map[string]any{
				"metadata": map[string]any{
					"name": "web",
					"annotations": map[string]any{
						"kubectl.kubernetes.io/last-applied-configuration": `{"big":"json"}`,
						"app.kubernetes.io/name":                           "web",
					},
				},
			},
			expected: map[string]any{
				"metadata": map[string]any{
					"name": "web",
					"annotations": map[string]any{
						"app.kubernetes.io/name": "web",
					},
				},
			},
		},
		{
			name: "removes both managedFields and last-applied-configuration",
			input: map[string]any{
				"metadata": map[string]any{
					"name":          "web",
					"managedFields": []any{"field1"},
					"annotations": map[string]any{
						"kubectl.kubernetes.io/last-applied-configuration": `{"big":"json"}`,
						"keep": "this",
					},
				},
				"spec": map[string]any{"replicas": 3},
			},
			expected: map[string]any{
				"metadata": map[string]any{
					"name": "web",
					"annotations": map[string]any{
						"keep": "this",
					},
				},
				"spec": map[string]any{"replicas": 3},
			},
		},
		{
			name: "no annotations to clean",
			input: map[string]any{
				"metadata": map[string]any{
					"name": "web",
				},
			},
			expected: map[string]any{
				"metadata": map[string]any{
					"name": "web",
				},
			},
		},
		{
			name:     "empty object",
			input:    map[string]any{},
			expected: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanObject(tt.input)
			assert.Equal(t, tt.expected, tt.input)
		})
	}
}
