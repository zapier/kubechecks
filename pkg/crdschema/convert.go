package crdschema

import (
	"strings"
)

// Keywords whose values are themselves schemas, or collections of schemas. Everything
// else in a schema object — `default`, `example`, `enum`, `required`, ... — is arbitrary
// data and must be copied through untouched, or we would happily rewrite a user's
// `default: {properties: ...}` value as if it were a schema.
var (
	schemaKeys     = map[string]struct{}{"not": {}, "additionalItems": {}, "contains": {}, "propertyNames": {}, "if": {}, "then": {}, "else": {}}
	schemaMapKeys  = map[string]struct{}{"properties": {}, "patternProperties": {}, "definitions": {}}
	schemaListKeys = map[string]struct{}{"allOf": {}, "anyOf": {}, "oneOf": {}}
)

// convert rewrites a CRD's openAPIV3Schema into a JSON Schema that kubeconform can
// compile. The two dialects are close but not identical, and Kubernetes layers its own
// extensions on top:
//
//   - objects that declare properties gain `additionalProperties: false`, so that strict
//     validation reports typo'd fields. The root object is exempt: it only ever describes
//     `spec`/`status`, while the manifest being checked also carries `apiVersion`, `kind`
//     and `metadata`.
//   - `x-kubernetes-preserve-unknown-fields` opts a subtree back out of that, matching how
//     the API server treats it.
//   - `x-kubernetes-int-or-string` (and OpenAPI's `int-or-string` format) becomes an anyOf
//     over string and integer.
//   - `nullable: true` widens the declared type to also admit null, which is how OpenAPI
//     spells what JSON Schema expresses as a type list.
//   - the remaining `x-kubernetes-*` keys are dropped: they carry server-side semantics
//     (pruning, merge strategy, validation rules) that mean nothing to a JSON Schema
//     validator, and leaving them in only makes the generated file harder to read.
func convert(node any, isRoot bool) any {
	switch n := node.(type) {
	case map[string]any:
		return convertObject(n, isRoot)
	default:
		return node
	}
}

func convertObject(in map[string]any, isRoot bool) map[string]any {
	var (
		out             = make(map[string]any, len(in))
		preserveUnknown bool
		intOrString     bool
		nullable        bool
	)

	for key, value := range in {
		switch key {
		case "x-kubernetes-preserve-unknown-fields":
			preserveUnknown, _ = value.(bool)
			continue
		case "x-kubernetes-int-or-string":
			intOrString, _ = value.(bool)
			continue
		case "nullable":
			nullable, _ = value.(bool)
			continue
		case "required":
			// draft-04 rejects an empty `required`, and an empty one constrains nothing
			// anyway. Some generators emit it, so drop it rather than fail to compile.
			if list, ok := value.([]any); ok && len(list) == 0 {
				continue
			}
		case "items":
			// Either a single schema or, in draft-04, a list of them.
			if list, ok := value.([]any); ok {
				out[key] = convertSchemaList(list)
			} else {
				out[key] = convert(value, false)
			}
			continue
		case "additionalProperties":
			// Either a schema or a bool.
			if sub, ok := value.(map[string]any); ok {
				out[key] = convertObject(sub, false)
				continue
			}
		case "dependencies":
			// Maps a property name to either a schema or a list of property names.
			if deps, ok := value.(map[string]any); ok {
				converted := make(map[string]any, len(deps))
				for name, dep := range deps {
					if sub, ok := dep.(map[string]any); ok {
						converted[name] = convertObject(sub, false)
					} else {
						converted[name] = dep
					}
				}
				out[key] = converted
				continue
			}
		}

		if strings.HasPrefix(key, "x-kubernetes-") {
			continue
		}

		switch {
		case isIn(schemaKeys, key):
			out[key] = convert(value, false)
		case isIn(schemaMapKeys, key):
			out[key] = convertSchemaMap(value)
		case isIn(schemaListKeys, key):
			out[key] = convertSchemaList(value)
		default:
			out[key] = value
		}
	}

	if format, _ := out["format"].(string); format == "int-or-string" {
		delete(out, "format")
		intOrString = true
	}

	if intOrString {
		delete(out, "type")
		if _, ok := out["anyOf"]; !ok {
			out["anyOf"] = []any{
				map[string]any{"type": "string"},
				map[string]any{"type": "integer"},
			}
		}
	}

	if nullable {
		if declared, ok := out["type"].(string); ok && declared != "null" {
			out["type"] = []any{declared, "null"}
		}
	}

	if _, hasProperties := out["properties"]; hasProperties && !isRoot && !preserveUnknown {
		if _, alreadySet := out["additionalProperties"]; !alreadySet {
			out["additionalProperties"] = false
		}
	}

	return out
}

func convertSchemaMap(value any) any {
	schemas, ok := value.(map[string]any)
	if !ok {
		return value
	}

	out := make(map[string]any, len(schemas))
	for name, schema := range schemas {
		out[name] = convert(schema, false)
	}
	return out
}

func convertSchemaList(value any) any {
	schemas, ok := value.([]any)
	if !ok {
		return value
	}

	out := make([]any, len(schemas))
	for index, schema := range schemas {
		out[index] = convert(schema, false)
	}
	return out
}

func isIn(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}
