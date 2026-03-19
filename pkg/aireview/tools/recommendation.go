package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zapier/kubechecks/pkg/aireview"
)

var submitRecommendationSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"recommendation": {
			"type": "string",
			"enum": ["APPROVE", "WARN", "FLAG"],
			"description": "APPROVE = safe to merge, WARN = minor issues worth noting, FLAG = critical issues that should block merge"
		},
		"reason": {
			"type": "string",
			"description": "Brief explanation for this recommendation"
		},
		"source": {
			"type": "string",
			"description": "What check produced this recommendation (e.g. 'values validation', 'resource limits', 'impact analysis', 'security review')"
		}
	},
	"required": ["recommendation", "reason", "source"]
}`)

// SubmitRecommendationTool returns a tool that records a recommendation during a review.
// Call this tool for each distinct finding. Multiple recommendations can be submitted;
// the final commit status will be the worst across all recommendations.
func SubmitRecommendationTool(collector *aireview.RecommendationCollector) aireview.Tool {
	return aireview.NewTool(
		"submit_recommendation",
		"Submit a recommendation for this review. Call once per distinct finding. Multiple recommendations can be submitted — the final status will be the worst across all (FLAG > WARN > APPROVE). Use APPROVE for safe changes, WARN for minor issues, FLAG for critical problems that should block merge.",
		submitRecommendationSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Recommendation string `json:"recommendation"`
				Reason         string `json:"reason"`
				Source         string `json:"source"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			rec := strings.ToUpper(params.Recommendation)
			if rec != "APPROVE" && rec != "WARN" && rec != "FLAG" {
				return fmt.Sprintf("Error: recommendation must be APPROVE, WARN, or FLAG (got %q)", params.Recommendation), nil
			}
			if params.Reason == "" {
				return "Error: reason is required", nil
			}
			if params.Source == "" {
				return "Error: source is required", nil
			}

			collector.Add(aireview.RecommendationEntry{
				Recommendation: rec,
				Reason:         params.Reason,
				Source:         params.Source,
			})

			return fmt.Sprintf("Recommendation recorded: %s (%s). Total recommendations so far: %d", rec, params.Source, collector.Len()), nil
		},
	)
}
