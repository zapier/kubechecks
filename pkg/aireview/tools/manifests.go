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
		"Get the unified diff of manifest changes between the proposed (desired) state and the currently deployed (live) state (CRDs and Secrets excluded). Call this first to understand what changed.",
		emptyInputSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			if diff == "" {
				return "No changes detected.", nil
			}
			filtered := filterDiffSections(diff)
			if filtered == "" {
				return "No changes detected (CRDs and Secrets excluded).", nil
			}
			return filtered, nil
		},
	)
}

// filterDiffSections removes excluded resource kinds from the unified diff output.
// Diff sections are delimited by "===== group/kind namespace/name ======" headers.
// The header format is: "===== {Group}/{Kind} {Namespace}/{Name} ======"
func filterDiffSections(diff string) string {
	sections := strings.Split(diff, "===== ")
	var kept []string
	for _, section := range sections {
		if section == "" {
			continue
		}
		if kind := extractKindFromDiffHeader(section); excludedKinds[kind] {
			continue
		}
		kept = append(kept, "===== "+section)
	}
	return strings.Join(kept, "")
}

// extractKindFromDiffHeader parses the Kind from a diff section header.
// Header format: "{Group}/{Kind} {Namespace}/{Name} ======\n..."
// e.g. "/Secret default/my-secret ======" → "Secret"
// e.g. "apiextensions.k8s.io/CustomResourceDefinition /mycrd ======" → "CustomResourceDefinition"
func extractKindFromDiffHeader(section string) string {
	// Take the first line (the header)
	header := section
	if idx := strings.IndexByte(section, '\n'); idx >= 0 {
		header = section[:idx]
	}
	// Header is "{Group}/{Kind} {Namespace}/{Name} ======"
	// Split on space to get "{Group}/{Kind}"
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 0 {
		return ""
	}
	groupKind := parts[0]
	// Split on "/" to get Kind (last segment)
	if idx := strings.LastIndex(groupKind, "/"); idx >= 0 {
		return groupKind[idx+1:]
	}
	return groupKind
}

// maxManifestBytes is the max size of rendered manifests returned to the LLM to control token usage.
const maxManifestBytes = 100_000

// RenderedManifestsTool returns a tool that provides the full rendered YAML manifests.
// CRDs are filtered out (too large, not useful for review). Output is truncated if it exceeds the token budget.
func RenderedManifestsTool(yamlManifests []string) aireview.Tool {
	return aireview.NewTool(
		"get_rendered_manifests",
		"Get the full rendered YAML manifests that will be applied (CRDs and Secrets excluded). Use this when you need more context beyond the diff, such as checking resource requests/limits, probes, or other fields that were not changed but are relevant to the review.",
		emptyInputSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			filtered := filterManifests(yamlManifests)
			if len(filtered) == 0 {
				return "No manifests available (CRDs and Secrets excluded).", nil
			}
			result := strings.Join(filtered, "\n---\n")
			if len(result) > maxManifestBytes {
				result = result[:maxManifestBytes] + "\n\n[truncated — manifests exceeded size limit]"
			}
			return result, nil
		},
	)
}

// excludedKinds are resource kinds filtered from manifests sent to the LLM.
// CRDs are too large and not useful for review. Secrets may contain sensitive data.
var excludedKinds = map[string]bool{
	"CustomResourceDefinition": true,
	"Secret":                   true,
}

// filterManifests removes excluded resource kinds from the manifest list.
func filterManifests(manifests []string) []string {
	var filtered []string
	for _, m := range manifests {
		if isExcludedKind(m) {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered
}

// isExcludedKind checks if a YAML manifest is a kind that should be excluded from LLM context.
func isExcludedKind(manifest string) bool {
	var meta typeMeta
	if err := yaml.Unmarshal([]byte(manifest), &meta); err != nil {
		return false
	}
	return excludedKinds[meta.Kind]
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
