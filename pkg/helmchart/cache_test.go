package helmchart

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCache_ListAndReadFiles(t *testing.T) {
	// Create a fake cached chart on disk
	tmpDir := t.TempDir()
	cache, err := NewCache(tmpDir)
	require.NoError(t, err)

	chartDir := filepath.Join(tmpDir, "testchart", "1.0.0")
	require.NoError(t, os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: testchart\nversion: 1.0.0"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("replicaCount: 1\nimage:\n  tag: latest"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "templates", "deployment.yaml"), []byte("kind: Deployment"), 0o644))

	// List files
	files, err := cache.ListFiles("testchart", "1.0.0")
	require.NoError(t, err)
	assert.Contains(t, files, "Chart.yaml")
	assert.Contains(t, files, "values.yaml")
	assert.Contains(t, files, filepath.Join("templates", "deployment.yaml"))

	// Read file
	content, err := cache.ReadFile("testchart", "1.0.0", "values.yaml")
	require.NoError(t, err)
	assert.Contains(t, content, "replicaCount: 1")

	// Read nested file
	content, err = cache.ReadFile("testchart", "1.0.0", "templates/deployment.yaml")
	require.NoError(t, err)
	assert.Contains(t, content, "Deployment")
}

func TestCache_ReadFile_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewCache(tmpDir)
	require.NoError(t, err)

	_, err = cache.ReadFile("nonexistent", "1.0.0", "values.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCache_ReadFile_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewCache(tmpDir)
	require.NoError(t, err)

	// Create a chart dir
	chartDir := filepath.Join(tmpDir, "testchart", "1.0.0")
	require.NoError(t, os.MkdirAll(chartDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("ok"), 0o644))

	// Try path traversal
	_, err = cache.ReadFile("testchart", "1.0.0", "../../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid path")
}

func TestCache_ListFiles_NotCached(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewCache(tmpDir)
	require.NoError(t, err)

	_, err = cache.ListFiles("nonexistent", "1.0.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not cached")
}

func TestCache_EnsureChart_CacheHit(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewCache(tmpDir)
	require.NoError(t, err)

	// Pre-create cached chart
	chartDir := filepath.Join(tmpDir, "testchart", "1.0.0")
	require.NoError(t, os.MkdirAll(chartDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("ok"), 0o644))

	// Should return cached path without downloading
	path, err := cache.EnsureChart("https://example.com/charts", "testchart", "1.0.0")
	require.NoError(t, err)
	assert.Equal(t, chartDir, path)
}
