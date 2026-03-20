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
5. Assess impact — what could go wrong? what is the blast radius?
6. Recommend — approve, warn, or flag with specific reasoning

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

### Code Suggestions
When you find an issue that has a concrete fix (e.g., misspelled key, wrong value, missing field), use the post_suggestion tool to propose the corrected code. The suggestion will appear as an "Apply suggestion" button in the PR that the reviewer can click to commit the fix directly. Use the exact file path and line numbers from the "Changed Files" section above.

### Recommendation
Use the submit_recommendation tool for EACH distinct finding during your review. Call it multiple times if you find multiple issues. The final commit status will be the worst across all recommendations (FLAG > WARN > APPROVE).

- APPROVE: safe to merge, no significant issues
- WARN: minor issues worth noting but not blocking
- FLAG: critical issues that should block merge

You MUST call submit_recommendation at least once before completing your review.
When a recommendation is WARN or FLAG and the fix is known, you MUST also call post_suggestion with the corrected code.`

// BuildSystemPrompt creates the system prompt for a review.
// The environment context (app name, namespace, etc.) is always prepended.
// If customPrompt is non-empty, it replaces the default review instructions.
func BuildSystemPrompt(appName, namespace, cluster, k8sVersion, customPrompt, extraInstructions string) string {
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

	result := envContext + "\n\n" + reviewPrompt

	if extraInstructions != "" {
		result += "\n\n## Additional Instructions\n\n" + extraInstructions
	}

	return result
}

// BuildUserPrompt creates the initial user message for the review agent.
// Includes the diff, rendered manifests, Helm values, and changed files inline so the LLM can start reviewing immediately.
func BuildUserPrompt(appName string, diff string, renderedManifests string, helmValues string, changedFiles string, toolNames []string) string {
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

	if changedFiles != "" {
		sb.WriteString(changedFiles)
		sb.WriteString("\n\n")
	}

	if len(toolNames) > 0 {
		sb.WriteString("Additional tools available for deeper investigation: ")
		sb.WriteString(strings.Join(toolNames, ", "))
		sb.WriteString("\n\n")
	}

	sb.WriteString("Assess the impact of the changes and provide your review.")
	return sb.String()
}
