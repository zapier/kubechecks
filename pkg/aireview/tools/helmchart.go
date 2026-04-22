package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zapier/kubechecks/pkg/aireview"
	"github.com/zapier/kubechecks/pkg/helmchart"
)

// ChartSource maps a chart name to its repository URL.
type ChartSource struct {
	Name       string
	Version    string
	Repository string
}

var listChartFilesSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"chart": {
			"type": "string",
			"description": "Chart name (e.g. 'podinfo', 'echo-server')"
		},
		"version": {
			"type": "string",
			"description": "Chart version (e.g. '6.9.1', '0.5.0')"
		}
	},
	"required": ["chart", "version"]
}`)

var readChartFileSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"chart": {
			"type": "string",
			"description": "Chart name (e.g. 'podinfo', 'echo-server')"
		},
		"version": {
			"type": "string",
			"description": "Chart version (e.g. '6.9.1', '0.5.0')"
		},
		"path": {
			"type": "string",
			"description": "File path relative to chart root (e.g. 'values.yaml', 'templates/deployment.yaml', 'Chart.yaml')"
		}
	},
	"required": ["chart", "version", "path"]
}`)

// ListChartFilesTool returns a tool that lists files in a Helm chart.
// chartSources maps chart names to their repo URLs for downloading.
func ListChartFilesTool(cache *helmchart.Cache, chartSources []ChartSource) aireview.Tool {
	repoMap := buildRepoMap(chartSources)
	availableCharts := formatAvailableCharts(chartSources)

	return aireview.NewTool(
		"list_chart_files",
		fmt.Sprintf("List all files in a Helm chart. Available charts: %s. Use this to discover files (values.yaml, templates/, helpers) before reading them.", availableCharts),
		listChartFilesSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Chart   string `json:"chart"`
				Version string `json:"version"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			repoURL, ok := repoMap[params.Chart]
			if !ok {
				return fmt.Sprintf("Unknown chart %q. Available charts: %s", params.Chart, availableCharts), nil
			}

			if _, err := cache.EnsureChart(repoURL, params.Chart, params.Version); err != nil {
				return fmt.Sprintf("Failed to download chart %s@%s: %s", params.Chart, params.Version, err), nil
			}

			files, err := cache.ListFiles(params.Chart, params.Version)
			if err != nil {
				return "", fmt.Errorf("failed to list chart files: %w", err)
			}

			return strings.Join(files, "\n"), nil
		},
	)
}

// ReadChartFileTool returns a tool that reads a file from a Helm chart.
// chartSources maps chart names to their repo URLs for downloading.
func ReadChartFileTool(cache *helmchart.Cache, chartSources []ChartSource) aireview.Tool {
	repoMap := buildRepoMap(chartSources)
	availableCharts := formatAvailableCharts(chartSources)

	return aireview.NewTool(
		"read_chart_file",
		fmt.Sprintf("Read a file from a Helm chart. Available charts: %s. Use to inspect values.yaml (detect misspelled values), values.schema.json (validate types), templates/*.yaml (understand template logic), _helpers.tpl (named templates).", availableCharts),
		readChartFileSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Chart   string `json:"chart"`
				Version string `json:"version"`
				Path    string `json:"path"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			repoURL, ok := repoMap[params.Chart]
			if !ok {
				return fmt.Sprintf("Unknown chart %q. Available charts: %s", params.Chart, availableCharts), nil
			}

			if _, err := cache.EnsureChart(repoURL, params.Chart, params.Version); err != nil {
				return fmt.Sprintf("Failed to download chart %s@%s: %s", params.Chart, params.Version, err), nil
			}

			content, err := cache.ReadFile(params.Chart, params.Version, params.Path)
			if err != nil {
				return fmt.Sprintf("File not found: %s", err), nil
			}

			if len(content) > maxManifestBytes {
				content = content[:maxManifestBytes] + "\n\n[truncated — file exceeded size limit]"
			}

			return content, nil
		},
	)
}

func buildRepoMap(sources []ChartSource) map[string]string {
	m := make(map[string]string, len(sources))
	for _, s := range sources {
		m[s.Name] = s.Repository
	}
	return m
}

func formatAvailableCharts(sources []ChartSource) string {
	parts := make([]string, len(sources))
	for i, s := range sources {
		parts[i] = fmt.Sprintf("%s@%s", s.Name, s.Version)
	}
	return strings.Join(parts, ", ")
}
