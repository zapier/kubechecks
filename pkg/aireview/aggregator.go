package aireview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/aiproviders"
)

const aggregatorPrompt = `You are consolidating multiple AI review reports for a single pull request.
Multiple ArgoCD applications were reviewed independently and produced overlapping findings because they share the same source files (e.g., values.yaml).

Your job:
1. Identify findings that appear across multiple apps (shared source files, same values.yaml issues, same misspelled keys)
   → Group these under "### Shared Findings" with a single clear description. Do NOT repeat the same finding for each app.
2. Keep app-specific findings separate (scaling impact, cluster-specific configs, resource usage, different replica counts)
   → Group these under "### App-Specific: <app-name>" — only include genuinely app-specific issues here.
3. Deduplicate recommendations — the same issue flagged by multiple apps should appear once.
4. Preserve the recommendation chain tables from each review — merge them into one consolidated table, removing duplicate entries.
5. Be concise — remove redundant explanations. If 3 apps all flag "replicaCounts is misspelled", say it once.
6. Do NOT add new findings or analysis — only consolidate what was already found.

Output the consolidated review as markdown, starting directly with the content (no preamble).`

// AggregateReviews consolidates multiple per-app review results into a single concise review.
// Uses a cheap/fast LLM call since it's text processing with no tool calls.
// Returns the consolidated review text. If aggregation fails, returns the raw concatenation as fallback.
func AggregateReviews(ctx context.Context, provider aiproviders.Provider, model string, appReviews map[string]string) (string, error) {
	if len(appReviews) <= 1 {
		// Single app — no aggregation needed
		for _, review := range appReviews {
			return review, nil
		}
		return "", nil
	}

	log.Info().Int("apps", len(appReviews)).Msg("aggregating AI reviews across apps")

	// Build the user prompt with all raw reviews
	var sb strings.Builder
	sb.WriteString("Here are the individual AI review reports to consolidate:\n\n")
	for appName, review := range appReviews {
		fmt.Fprintf(&sb, "---\n## Review for `%s`\n\n%s\n\n", appName, review)
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	resp, err := provider.Chat(ctx, aiproviders.ChatRequest{
		Model:        model,
		SystemPrompt: aggregatorPrompt,
		Messages: []aiproviders.Message{
			{Role: aiproviders.RoleUser, Text: sb.String()},
		},
		MaxTokens:   4096,
		Temperature: 0.1,
	})
	if err != nil {
		log.Warn().Err(err).Msg("aggregation failed, using raw reviews as fallback")
		return buildFallbackReview(appReviews), nil
	}

	log.Info().Int("apps", len(appReviews)).Msg("AI review aggregation complete")
	return resp.Text, nil
}

// buildFallbackReview concatenates raw reviews when aggregation fails.
func buildFallbackReview(appReviews map[string]string) string {
	var sb strings.Builder
	for appName, review := range appReviews {
		fmt.Fprintf(&sb, "### `%s`\n\n%s\n\n---\n\n", appName, review)
	}
	return sb.String()
}
