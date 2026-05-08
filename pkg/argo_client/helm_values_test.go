package argo_client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainsGlob(t *testing.T) {
	testcases := map[string]struct {
		path     string
		expected bool
	}{
		"plain":            {"values.yaml", false},
		"relative-plain":   {"./values.yaml", false},
		"parent-plain":     {"../values.yaml", false},
		"star":             {"./values-*.yaml", true},
		"parent-star":      {"../values-*.yaml", true},
		"question":         {"values-?.yaml", true},
		"char-class":       {"values-[ab].yaml", true},
		"empty":            {"", false},
		"directory-glob":   {"envs/*/values.yaml", true},
		"trailing-star":    {"values.yaml*", true},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, containsGlob(tc.path))
		})
	}
}

func TestCopyGlobValueFiles(t *testing.T) {
	// helper to lay out a fake repo structure under a temp dir
	setup := func(t *testing.T, files map[string]string) (srcAppPath, destAppDir string) {
		t.Helper()
		root := t.TempDir()
		srcAppPath = filepath.Join(root, "repo", "app1")
		destAppDir = filepath.Join(root, "package", "app1")

		require.NoError(t, os.MkdirAll(srcAppPath, 0o755))
		require.NoError(t, os.MkdirAll(destAppDir, 0o755))

		for relPath, content := range files {
			full := filepath.Join(srcAppPath, relPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
			require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
		}
		return srcAppPath, destAppDir
	}

	t.Run("expands glob within source path", func(t *testing.T) {
		srcAppPath, destAppDir := setup(t, map[string]string{
			"values-clusterA.yaml": "a",
			"values-clusterB.yaml": "b",
			"unrelated.yaml":       "c",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(srcAppPath, destAppDir, source, "./values-*.yaml")
		require.NoError(t, err)

		assertFileContent(t, filepath.Join(destAppDir, "values-clusterA.yaml"), "a")
		assertFileContent(t, filepath.Join(destAppDir, "values-clusterB.yaml"), "b")
		assertFileMissing(t, filepath.Join(destAppDir, "unrelated.yaml"))
	})

	t.Run("expands glob in sibling directory", func(t *testing.T) {
		srcAppPath, destAppDir := setup(t, map[string]string{
			"../shared/values-1.yaml": "one",
			"../shared/values-2.yaml": "two",
			"../shared/other.yaml":    "skip",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(srcAppPath, destAppDir, source, "../shared/values-*.yaml")
		require.NoError(t, err)

		assertFileContent(t, filepath.Join(destAppDir, "../shared/values-1.yaml"), "one")
		assertFileContent(t, filepath.Join(destAppDir, "../shared/values-2.yaml"), "two")
		assertFileMissing(t, filepath.Join(destAppDir, "../shared/other.yaml"))
	})

	t.Run("no matches with IgnoreMissingValueFiles is not an error", func(t *testing.T) {
		srcAppPath, destAppDir := setup(t, map[string]string{
			"values.yaml": "x",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{IgnoreMissingValueFiles: true},
		}
		err := copyGlobValueFiles(srcAppPath, destAppDir, source, "./missing-*.yaml")
		require.NoError(t, err)
	})

	t.Run("no matches without IgnoreMissingValueFiles returns an error", func(t *testing.T) {
		srcAppPath, destAppDir := setup(t, map[string]string{
			"values.yaml": "x",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(srcAppPath, destAppDir, source, "./missing-*.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched no files")
	})

	t.Run("no matches with nil Helm returns an error", func(t *testing.T) {
		srcAppPath, destAppDir := setup(t, map[string]string{
			"values.yaml": "x",
		})

		source := v1alpha1.ApplicationSource{}
		err := copyGlobValueFiles(srcAppPath, destAppDir, source, "./missing-*.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched no files")
	})

	t.Run("preserves nested directory layout", func(t *testing.T) {
		srcAppPath, destAppDir := setup(t, map[string]string{
			"envs/dev/values.yaml":     "dev",
			"envs/staging/values.yaml": "staging",
			"envs/dev/skip.txt":        "skip",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(srcAppPath, destAppDir, source, "./envs/*/values.yaml")
		require.NoError(t, err)

		assertFileContent(t, filepath.Join(destAppDir, "envs/dev/values.yaml"), "dev")
		assertFileContent(t, filepath.Join(destAppDir, "envs/staging/values.yaml"), "staging")
		assertFileMissing(t, filepath.Join(destAppDir, "envs/dev/skip.txt"))
	})
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "expected file %s to exist", path)
	assert.Equal(t, expected, string(data))
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "expected file %s to be missing", path)
}
