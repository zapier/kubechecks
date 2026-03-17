package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ghodss/yaml"

	"github.com/zapier/kubechecks/pkg/aireview"
)

// typeMeta is a minimal struct to unmarshal just the kind from a YAML manifest.
type typeMeta struct {
	Kind string `json:"kind" yaml:"kind"`
}

// emptyInputSchema is in place as tools does not accept any inputs from the LLM.
var emptyInputSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// DiffTool returns a tool that provides the unified diff text.
// CRD sections are filtered out. The diff is captured by closure from the caller's scope.
func DiffTool(diff string) aireview.Tool {
	return aireview.NewTool(
		"get_diff",
		"Get the unified diff of manifest changes between the proposed (desired) state and the currently deployed (live) state (CRDs excluded). Call this first to understand what changed.",
		emptyInputSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			if diff == "" {
				return "No changes detected.", nil
			}
			filtered := filterDiffSections(diff)
			if filtered == "" {
				return "No non-CRD changes detected.", nil
			}
			return filtered, nil
		},
	)
}

// filterDiffSections removes CRD resource sections from the unified diff output.
// Diff sections are delimited by "===== group/kind namespace/name ======" headers.
func filterDiffSections(diff string) string {
	sections := strings.Split(diff, "===== ")
	var kept []string
	for _, section := range sections {
		if section == "" {
			continue
		}
		if strings.Contains(section, "CustomResourceDefinition") {
			continue
		}
		kept = append(kept, "===== "+section)
	}
	return strings.Join(kept, "")
}

// maxManifestBytes is the max size of rendered manifests returned to the LLM to control token usage.
const maxManifestBytes = 100_000

// RenderedManifestsTool returns a tool that provides the full rendered YAML manifests.
// CRDs are filtered out (too large, not useful for review). Output is truncated if it exceeds the token budget.
func RenderedManifestsTool(yamlManifests []string) aireview.Tool {
	return aireview.NewTool(
		"get_rendered_manifests",
		"Get the full rendered YAML manifests that will be applied (CRDs excluded). Use this when you need more context beyond the diff, such as checking resource requests/limits, probes, or other fields that were not changed but are relevant to the review.",
		emptyInputSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			filtered := filterManifests(yamlManifests)
			if len(filtered) == 0 {
				return "No manifests available (CRDs were excluded).", nil
			}
			result := strings.Join(filtered, "\n---\n")
			if len(result) > maxManifestBytes {
				result = result[:maxManifestBytes] + "\n\n[truncated — manifests exceeded size limit]"
			}
			return result, nil
		},
	)
}

// filterManifests removes CRDs from the manifest list.
func filterManifests(manifests []string) []string {
	var filtered []string
	for _, m := range manifests {
		if isCRD(m) {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered
}

// isCRD checks if a YAML manifest is a CustomResourceDefinition by parsing the kind field.
func isCRD(manifest string) bool {
	var meta typeMeta
	if err := yaml.Unmarshal([]byte(manifest), &meta); err != nil {
		return false
	}
	return meta.Kind == "CustomResourceDefinition"
}

// AppInfoTool returns a tool that provides the ArgoCD application spec.
// The app info is captured by closure from the caller's scope.
func AppInfoTool(appName, namespace, cluster, project string, sourceInfo string) aireview.Tool {
	return aireview.NewTool(
		"get_app_info",
		"Get the ArgoCD application metadata: name, namespace, destination cluster, project, and source configuration (Helm chart, Kustomize, etc).",
		emptyInputSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			info := map[string]string{
				"name":      appName,
				"namespace": namespace,
				"cluster":   cluster,
				"project":   project,
				"source":    sourceInfo,
			}
			b, err := json.Marshal(info)
			if err != nil {
				return "", fmt.Errorf("failed to marshal app info: %w", err)
			}
			return string(b), nil
		},
	)
}
