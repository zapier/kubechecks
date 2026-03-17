package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusRangeQueryTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/query_range", r.URL.Path)
		assert.Equal(t, "up", r.URL.Query().Get("query"))
		assert.NotEmpty(t, r.URL.Query().Get("start"))
		assert.NotEmpty(t, r.URL.Query().Get("end"))
		assert.Equal(t, "5m", r.URL.Query().Get("step"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer server.Close()

	tool := PrometheusRangeQueryTool(server.URL)
	assert.Equal(t, "query_prometheus", tool.Def.Name)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"up"}`))
	require.NoError(t, err)
	assert.Contains(t, result, "success")
}

func TestPrometheusInstantQueryTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/query", r.URL.Path)
		assert.Equal(t, "kube_pod_info", r.URL.Query().Get("query"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1234,"42"]}]}}`))
	}))
	defer server.Close()

	tool := PrometheusInstantQueryTool(server.URL)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"kube_pod_info"}`))
	require.NoError(t, err)
	assert.Contains(t, result, "42")
}

func TestPrometheusRangeQueryTool_WithParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "1h", r.URL.Query().Get("step"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	tool := PrometheusRangeQueryTool(server.URL)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"up","start":"-7d","step":"1h"}`))
	require.NoError(t, err)
	assert.Contains(t, result, "success")
}

func TestPrometheusRangeQueryTool_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"status":"error","error":"bad query"}`))
	}))
	defer server.Close()

	tool := PrometheusRangeQueryTool(server.URL)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"up"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestResolveTime(t *testing.T) {
	now := time.Now()
	fallback := now.Add(-1 * time.Hour)

	// Empty string returns fallback
	result := resolveTime("", fallback)
	assert.Equal(t, fallback, result)

	// "now" returns fallback (which is the default)
	result = resolveTime("now", now)
	assert.Equal(t, now, result)

	// Relative -7d
	result = resolveTime("-7d", fallback)
	assert.True(t, result.Before(now))
	assert.WithinDuration(t, now.Add(-7*24*time.Hour), result, 2*time.Second)

	// Relative -1h
	result = resolveTime("-1h", fallback)
	assert.WithinDuration(t, now.Add(-1*time.Hour), result, 2*time.Second)

	// RFC3339
	fixed := "2024-01-15T10:00:00Z"
	result = resolveTime(fixed, fallback)
	expected, _ := time.Parse(time.RFC3339, fixed)
	assert.Equal(t, expected, result)
}
