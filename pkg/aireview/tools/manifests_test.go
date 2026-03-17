package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffTool(t *testing.T) {
	diff := "===== apps/Deployment default/web ======\n-replicas: 2\n+replicas: 5"
	tool := DiffTool(diff)

	assert.Equal(t, "get_diff", tool.Def.Name)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Contains(t, result, "replicas: 5")
}

func TestDiffTool_Empty(t *testing.T) {
	tool := DiffTool("")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, "No changes detected.", result)
}

func TestDiffTool_FiltersCRDs(t *testing.T) {
	diff := "===== apiextensions.k8s.io/CustomResourceDefinition /mycrd ======\n+some crd stuff\n===== apps/Deployment default/web ======\n-replicas: 2\n+replicas: 5"
	tool := DiffTool(diff)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.NotContains(t, result, "CustomResourceDefinition")
	assert.Contains(t, result, "replicas: 5")
}

func TestRenderedManifestsTool(t *testing.T) {
	manifests := []string{
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web",
		"apiVersion: v1\nkind: Service\nmetadata:\n  name: web",
	}
	tool := RenderedManifestsTool(manifests)

	assert.Equal(t, "get_rendered_manifests", tool.Def.Name)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Contains(t, result, "Deployment")
	assert.Contains(t, result, "Service")
	assert.Contains(t, result, "---")
}

func TestRenderedManifestsTool_FiltersCRDs(t *testing.T) {
	manifests := []string{
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web",
		"apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: mycrd",
	}
	tool := RenderedManifestsTool(manifests)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Contains(t, result, "Deployment")
	assert.NotContains(t, result, "CustomResourceDefinition")
}

func TestRenderedManifestsTool_Empty(t *testing.T) {
	tool := RenderedManifestsTool(nil)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Contains(t, result, "No manifests available")
}

func TestRenderedManifestsTool_Truncation(t *testing.T) {
	// Create a manifest larger than maxManifestBytes
	large := strings.Repeat("a", maxManifestBytes+1000)
	tool := RenderedManifestsTool([]string{large})

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Contains(t, result, "[truncated")
	assert.LessOrEqual(t, len(result), maxManifestBytes+100) // some slack for the truncation message
}

func TestAppInfoTool(t *testing.T) {
	tool := AppInfoTool("web-app", "production", "prod-cluster", "default", "repo=https://github.com/org/repo, path=charts/web")

	assert.Equal(t, "get_app_info", tool.Def.Name)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Contains(t, result, "web-app")
	assert.Contains(t, result, "production")
	assert.Contains(t, result, "prod-cluster")
}
