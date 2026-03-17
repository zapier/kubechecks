package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/zapier/kubechecks/pkg/aireview"
)

var queryPrometheusSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"query": {
			"type": "string",
			"description": "PromQL expression to execute"
		},
		"start": {
			"type": "string",
			"description": "Start time as RFC3339 or relative like '-7d', '-24h'. Defaults to '-1h'"
		},
		"end": {
			"type": "string",
			"description": "End time as RFC3339 or 'now'. Defaults to 'now'"
		},
		"step": {
			"type": "string",
			"description": "Query resolution step, e.g. '5m', '1h'. Defaults to '5m'"
		}
	},
	"required": ["query"]
}`)

var queryPrometheusInstantSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"query": {
			"type": "string",
			"description": "PromQL expression to execute as an instant query"
		}
	},
	"required": ["query"]
}`)

// PrometheusRangeQueryTool returns a tool that executes PromQL range queries against Prometheus/Thanos.
func PrometheusRangeQueryTool(prometheusURL string) aireview.Tool {
	return aireview.NewTool(
		"query_prometheus",
		"Execute a PromQL range query against Prometheus/Thanos. Returns time series data. Use for historical metrics like CPU/memory usage, replica counts, traffic patterns over time.",
		queryPrometheusSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Query string `json:"query"`
				Start string `json:"start"`
				End   string `json:"end"`
				Step  string `json:"step"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			now := time.Now()
			start := resolveTime(params.Start, now.Add(-1*time.Hour))
			end := resolveTime(params.End, now)
			step := params.Step
			if step == "" {
				step = "5m"
			}

			u, err := url.Parse(prometheusURL + "/api/v1/query_range")
			if err != nil {
				return "", fmt.Errorf("invalid prometheus URL: %w", err)
			}
			q := u.Query()
			q.Set("query", params.Query)
			q.Set("start", start.Format(time.RFC3339))
			q.Set("end", end.Format(time.RFC3339))
			q.Set("step", step)
			u.RawQuery = q.Encode()

			return fetchPrometheus(ctx, u.String())
		},
	)
}

// PrometheusInstantQueryTool returns a tool that executes instant PromQL queries.
func PrometheusInstantQueryTool(prometheusURL string) aireview.Tool {
	return aireview.NewTool(
		"query_prometheus_instant",
		"Execute an instant PromQL query against Prometheus/Thanos. Returns current point-in-time values. Use for current replica counts, connection pool sizes, queue depths.",
		queryPrometheusInstantSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			u, err := url.Parse(prometheusURL + "/api/v1/query")
			if err != nil {
				return "", fmt.Errorf("invalid prometheus URL: %w", err)
			}
			q := u.Query()
			q.Set("query", params.Query)
			u.RawQuery = q.Encode()

			return fetchPrometheus(ctx, u.String())
		},
	)
}

func fetchPrometheus(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("prometheus query failed: %w", err)
	}
	defer resp.Body.Close()

	// Limit response size to avoid excessive token usage
	body, err := io.ReadAll(io.LimitReader(resp.Body, 100_000))
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("prometheus returned status %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

// resolveTime parses a time string that can be RFC3339, relative (e.g., "-7d", "-24h"), or "now".
func resolveTime(s string, defaultTime time.Time) time.Time {
	if s == "" || s == "now" {
		return defaultTime
	}

	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}

	// Try relative duration (e.g., "-7d", "-24h", "-30m")
	if len(s) > 1 && s[0] == '-' {
		suffix := s[len(s)-1]
		numStr := s[1 : len(s)-1]
		var multiplier time.Duration
		switch suffix {
		case 'd':
			multiplier = 24 * time.Hour
		case 'h':
			multiplier = time.Hour
		case 'm':
			multiplier = time.Minute
		}
		if multiplier > 0 {
			var n int
			if _, err := fmt.Sscanf(numStr, "%d", &n); err == nil {
				return time.Now().Add(-time.Duration(n) * multiplier)
			}
		}
	}

	return defaultTime
}
