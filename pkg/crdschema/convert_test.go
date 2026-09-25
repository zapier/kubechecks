package crdschema

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertClosesNestedObjectsButNotTheRoot(t *testing.T) {
	// The root describes only spec/status, while the manifest being validated also
	// carries apiVersion, kind and metadata — closing it would reject every resource.
	converted := convert(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"spec": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"replicas": map[string]any{"type": "integer"},
				},
			},
		},
	}, true).(map[string]any)

	assert.NotContains(t, converted, "additionalProperties")

	spec := converted["properties"].(map[string]any)["spec"].(map[string]any)
	assert.Equal(t, false, spec["additionalProperties"])

	replicas := spec["properties"].(map[string]any)["replicas"].(map[string]any)
	assert.NotContains(t, replicas, "additionalProperties")
}

func TestConvertRespectsExplicitAdditionalProperties(t *testing.T) {
	converted := convert(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"labels": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"app": map[string]any{"type": "string"}},
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
	}, true).(map[string]any)

	labels := converted["properties"].(map[string]any)["labels"].(map[string]any)
	assert.Equal(t, map[string]any{"type": "string"}, labels["additionalProperties"])
}

func TestConvertHonorsPreserveUnknownFields(t *testing.T) {
	converted := convert(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"values": map[string]any{
				"type":                                 "object",
				"properties":                           map[string]any{"known": map[string]any{"type": "string"}},
				"x-kubernetes-preserve-unknown-fields": true,
			},
		},
	}, true).(map[string]any)

	values := converted["properties"].(map[string]any)["values"].(map[string]any)
	assert.NotContains(t, values, "additionalProperties", "a subtree that opts out of pruning must stay open")
	assert.NotContains(t, values, "x-kubernetes-preserve-unknown-fields")
}

func TestConvertIntOrString(t *testing.T) {
	anyOf := []any{
		map[string]any{"type": "string"},
		map[string]any{"type": "integer"},
	}

	t.Run("kubernetes extension", func(t *testing.T) {
		converted := convert(map[string]any{
			"type":                       "string",
			"x-kubernetes-int-or-string": true,
		}, false).(map[string]any)

		assert.NotContains(t, converted, "type")
		assert.Equal(t, anyOf, converted["anyOf"])
	})

	t.Run("openapi format", func(t *testing.T) {
		converted := convert(map[string]any{"format": "int-or-string"}, false).(map[string]any)

		assert.NotContains(t, converted, "format")
		assert.Equal(t, anyOf, converted["anyOf"])
	})

	t.Run("keeps an existing anyOf", func(t *testing.T) {
		existing := []any{map[string]any{"type": "integer"}}
		converted := convert(map[string]any{
			"x-kubernetes-int-or-string": true,
			"anyOf":                      existing,
		}, false).(map[string]any)

		assert.Len(t, converted["anyOf"], 1)
	})
}

func TestConvertNullableWidensType(t *testing.T) {
	converted := convert(map[string]any{"type": "string", "nullable": true}, false).(map[string]any)

	assert.Equal(t, []any{"string", "null"}, converted["type"])
	assert.NotContains(t, converted, "nullable")
}

func TestConvertDropsKubernetesExtensions(t *testing.T) {
	converted := convert(map[string]any{
		"type":                           "array",
		"x-kubernetes-list-type":         "map",
		"x-kubernetes-list-map-keys":     []any{"name"},
		"x-kubernetes-validations":       []any{map[string]any{"rule": "self.size() > 0"}},
		"x-kubernetes-embedded-resource": true,
	}, false).(map[string]any)

	assert.Equal(t, map[string]any{"type": "array"}, converted)
}

func TestConvertLeavesDataValuesAlone(t *testing.T) {
	// `default` and `enum` hold instance data, not schemas. Recursing into them would
	// rewrite a user's value as if it described a type.
	defaultValue := map[string]any{"properties": map[string]any{"nested": "value"}}

	converted := convert(map[string]any{
		"type":       "object",
		"properties": map[string]any{"config": map[string]any{"type": "object", "default": defaultValue}},
	}, true).(map[string]any)

	config := converted["properties"].(map[string]any)["config"].(map[string]any)
	assert.Equal(t, defaultValue, config["default"])
}

func TestConvertRecursesThroughCombinators(t *testing.T) {
	converted := convert(map[string]any{
		"oneOf": []any{
			map[string]any{
				"type":       "object",
				"properties": map[string]any{"a": map[string]any{"type": "string"}},
			},
		},
		"items": map[string]any{
			"type":       "object",
			"properties": map[string]any{"b": map[string]any{"type": "string"}},
		},
	}, true).(map[string]any)

	branch := converted["oneOf"].([]any)[0].(map[string]any)
	assert.Equal(t, false, branch["additionalProperties"])

	items := converted["items"].(map[string]any)
	assert.Equal(t, false, items["additionalProperties"])
}

func TestConvertDropsEmptyRequired(t *testing.T) {
	// draft-04 rejects an empty `required`, and the schema would fail to compile.
	converted := convert(map[string]any{"type": "object", "required": []any{}}, false).(map[string]any)
	assert.NotContains(t, converted, "required")

	kept := convert(map[string]any{"type": "object", "required": []any{"spec"}}, false).(map[string]any)
	assert.Equal(t, []any{"spec"}, kept["required"])
}

func TestSchemaFileName(t *testing.T) {
	// These names must match what kubeconform's registry asks its schema location for.
	assert.Equal(t, "widget-example-v1.json", SchemaFileName("Widget", "example.com", "v1"))
	assert.Equal(t, "certificate-cert-manager-v1.json", SchemaFileName("Certificate", "cert-manager.io", "v1"))
	assert.Equal(t, "application-argoproj-v1alpha1.json", SchemaFileName("Application", "argoproj.io", "v1alpha1"))
	assert.Equal(t, "thing-nodots-v1.json", SchemaFileName("Thing", "nodots", "v1"))
}
