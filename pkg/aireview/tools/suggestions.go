package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zapier/kubechecks/pkg/aireview"
)

var postSuggestionSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "File path in the PR (e.g. 'apps/echo-server/in-cluster/values.yaml'). Must be a file that was changed in this PR."
		},
		"start_line": {
			"type": "integer",
			"description": "Optional: first line number for a multi-line suggestion. Omit for single-line suggestions."
		},
		"end_line": {
			"type": "integer",
			"description": "Line number in the changed file where the suggestion applies. For multi-line, this is the last line."
		},
		"body": {
			"type": "string",
			"description": "Explanation of the issue (shown above the suggestion)."
		},
		"suggestion": {
			"type": "string",
			"description": "The corrected code that should replace the line(s). This will be shown as an 'Apply suggestion' button in the PR."
		}
	},
	"required": ["path", "end_line", "body", "suggestion"]
}`)

// PostSuggestionTool returns a tool that collects code suggestions during a review.
// Suggestions are NOT posted immediately — they are collected and batched into a single
// GitHub/GitLab review after the agent completes.
// changedFiles is the list of files changed in the PR, used to validate paths.
func PostSuggestionTool(collector *aireview.SuggestionCollector, changedFiles []string) aireview.Tool {
	changedSet := make(map[string]bool, len(changedFiles))
	for _, f := range changedFiles {
		changedSet[f] = true
	}

	return aireview.NewTool(
		"post_suggestion",
		fmt.Sprintf("Post a code suggestion on a specific file and line in the PR. The suggestion will appear as an 'Apply suggestion' button that the reviewer can click to commit the fix. Only works on files changed in this PR: %v", changedFiles),
		postSuggestionSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Path       string `json:"path"`
				StartLine  int    `json:"start_line"`
				EndLine    int    `json:"end_line"`
				Body       string `json:"body"`
				Suggestion string `json:"suggestion"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			if params.Path == "" {
				return "Error: 'path' is required.", nil
			}
			if params.EndLine <= 0 {
				return "Error: 'end_line' must be a positive integer.", nil
			}
			if params.Suggestion == "" {
				return "Error: 'suggestion' is required.", nil
			}

			if !changedSet[params.Path] {
				return fmt.Sprintf("Error: file %q is not part of this PR's changed files. Can only suggest changes to: %v", params.Path, changedFiles), nil
			}

			collector.Add(aireview.Suggestion{
				Path:       params.Path,
				StartLine:  params.StartLine,
				EndLine:    params.EndLine,
				Body:       params.Body,
				Suggestion: params.Suggestion,
			})

			return "Suggestion recorded. It will be posted as a review comment after the review completes.", nil
		},
	)
}
