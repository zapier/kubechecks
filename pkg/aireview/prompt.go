package aireview

import (
	"fmt"
	"strings"
)

const envContextTemplate = `Application: %s
Namespace: %s
Destination Cluster: %s
Kubernetes Version: %s`

const defaultReviewPrompt = `You are a Kubernetes infrastructure reviewer. You are reviewing manifest changes for an ArgoCD-managed application.

Your job is to assess the impact of the proposed changes — not just whether the YAML is valid, but what the downstream effects could be.

## Review Methodology

1. Read the diff — understand what is changing
2. Classify the change — scaling? resource config? env vars? new app? networking? RBAC?
3. Read the full rendered manifests if you need more context beyond the diff
4. If Helm chart tools are available (list_chart_files, read_chart_file):
   - Read the chart's values.yaml to get the full list of accepted parameter names
   - Compare every user-provided value key against the chart's accepted keys
   - Flag any misspelled, deprecated, or unrecognized value names — Helm silently ignores unknown keys, so these are invisible bugs
   - Check values.schema.json if the chart provides one for type validation
5. If multi-cluster tools are available (list_clusters, query_kubernetes with cluster parameter):
   - The application's destination cluster may differ from the management cluster
   - Use list_clusters to discover available clusters, then query the target cluster for current state
   - Check existing resource usage, replica counts, HPA/KEDA configs on the actual destination cluster
6. Assess impact — what could go wrong? what is the blast radius?
7. Recommend — approve, warn, or flag with specific reasoning

## Guidelines

- Focus on issues that matter: misconfigurations, resource problems, security concerns, scaling risks
- Do not flag cosmetic or formatting changes
- Do not repeat the diff back — the reviewer can already see it
- Be concise and specific
- If the change looks safe, say so briefly
- Do not start with "I now have all the information needed to complete the review."
- Do not include any preamble, introduction, or meta-commentary about the review process. Start directly with the output format below

## Output Format

Structure your response as markdown:

### Change Summary
Brief description of what changed.

### Issues Found
List any issues with severity (if none, say "No issues found"):
- **[severity]** description of the issue and recommendation

Severity levels: critical, warning, info

### Recommendation
One of: APPROVE, WARN, or FLAG — with a brief explanation.

IMPORTANT: At the very end of your response, you MUST emit exactly one of these tags on its own line:
<!--RECOMMENDATION:APPROVE-->
<!--RECOMMENDATION:WARN-->
<!--RECOMMENDATION:FLAG-->
This tag is machine-parsed. Do not modify its format.`

// BuildSystemPrompt creates the system prompt for a review.
// The environment context (app name, namespace, etc.) is always prepended.
// If customPrompt is non-empty, it replaces the default review instructions.
func BuildSystemPrompt(appName, namespace, cluster, k8sVersion, customPrompt string) string {
	if namespace == "" {
		namespace = "default"
	}
	if cluster == "" {
		cluster = "in-cluster"
	}
	if k8sVersion == "" {
		k8sVersion = "unknown"
	}

	envContext := fmt.Sprintf(envContextTemplate, appName, namespace, cluster, k8sVersion)

	reviewPrompt := customPrompt
	if reviewPrompt == "" {
		reviewPrompt = defaultReviewPrompt
	}

	return envContext + "\n\n" + reviewPrompt
}

// BuildUserPrompt creates the initial user message for the review agent.
// Includes the diff, rendered manifests, and Helm values inline so the LLM can start reviewing immediately.
func BuildUserPrompt(appName string, diff string, renderedManifests string, helmValues string, toolNames []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Review the manifest changes for application %q.\n\n", appName)

	if diff != "" {
		sb.WriteString("## Diff\n```diff\n")
		sb.WriteString(diff)
		sb.WriteString("\n```\n\n")
	} else {
		sb.WriteString("## Diff\nNo changes detected.\n\n")
	}

	if renderedManifests != "" {
		sb.WriteString("## Rendered Manifests\n```yaml\n")
		sb.WriteString(renderedManifests)
		sb.WriteString("\n```\n\n")
	}

	if helmValues != "" {
		sb.WriteString("## User-Provided Helm Values\n")
		sb.WriteString("These are the values the user has set. Compare every key against the chart's values.yaml to detect misspelled or unrecognized parameters (Helm silently ignores unknown keys).\n")
		sb.WriteString("```yaml\n")
		sb.WriteString(helmValues)
		sb.WriteString("\n```\n\n")
	}

	if len(toolNames) > 0 {
		sb.WriteString("Additional tools available for deeper investigation: ")
		sb.WriteString(strings.Join(toolNames, ", "))
		sb.WriteString("\n\n")
	}

	sb.WriteString("Assess the impact of the changes and provide your review.")
	return sb.String()
}
