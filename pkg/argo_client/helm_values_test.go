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
		"plain":              {"values.yaml", false},
		"relative-plain":     {"./values.yaml", false},
		"parent-plain":       {"../values.yaml", false},
		"star":               {"./values-*.yaml", true},
		"parent-star":        {"../values-*.yaml", true},
		"question":           {"values-?.yaml", true},
		"char-class":         {"values-[ab].yaml", true},
		"empty":              {"", false},
		"directory-glob":     {"envs/*/values.yaml", true},
		"trailing-star":      {"values.yaml*", true},
		"multi-wildcard":     {"./env-*/values-*.yaml", true},
		"numeric-char-class": {"values-[0-9].yaml", true},
		// Unclosed '[' is conservatively treated as a glob; filepath.Glob will
		// return ErrBadPattern if expansion is attempted.
		"unclosed-bracket": {"values-[ab.yaml", true},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, containsGlob(tc.path))
		})
	}
}

func TestCopyGlobValueFiles(t *testing.T) {
	// helper to lay out a fake repo structure under a temp dir
	setup := func(t *testing.T, files map[string]string) (repoRoot, destDir, srcAppPath, destAppDir string) {
		t.Helper()
		root := t.TempDir()
		repoRoot = filepath.Join(root, "repo")
		destDir = filepath.Join(root, "package")
		srcAppPath = filepath.Join(repoRoot, "app1")
		destAppDir = filepath.Join(destDir, "app1")

		require.NoError(t, os.MkdirAll(srcAppPath, 0o755))
		require.NoError(t, os.MkdirAll(destAppDir, 0o755))

		for relPath, content := range files {
			full := filepath.Join(srcAppPath, relPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
			require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
		}
		return repoRoot, destDir, srcAppPath, destAppDir
	}

	t.Run("expands glob within source path", func(t *testing.T) {
		repoRoot, destDir, srcAppPath, destAppDir := setup(t, map[string]string{
			"values-clusterA.yaml": "a",
			"values-clusterB.yaml": "b",
			"unrelated.yaml":       "c",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir, source, "./values-*.yaml")
		require.NoError(t, err)

		assertFileContent(t, filepath.Join(destAppDir, "values-clusterA.yaml"), "a")
		assertFileContent(t, filepath.Join(destAppDir, "values-clusterB.yaml"), "b")
		assertFileMissing(t, filepath.Join(destAppDir, "unrelated.yaml"))
	})

	t.Run("expands glob in sibling directory", func(t *testing.T) {
		repoRoot, destDir, srcAppPath, destAppDir := setup(t, map[string]string{
			"../shared/values-1.yaml": "one",
			"../shared/values-2.yaml": "two",
			"../shared/other.yaml":    "skip",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir, source, "../shared/values-*.yaml")
		require.NoError(t, err)

		assertFileContent(t, filepath.Join(destAppDir, "../shared/values-1.yaml"), "one")
		assertFileContent(t, filepath.Join(destAppDir, "../shared/values-2.yaml"), "two")
		assertFileMissing(t, filepath.Join(destAppDir, "../shared/other.yaml"))
	})

	t.Run("no matches with IgnoreMissingValueFiles is not an error", func(t *testing.T) {
		repoRoot, destDir, srcAppPath, destAppDir := setup(t, map[string]string{
			"values.yaml": "x",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{IgnoreMissingValueFiles: true},
		}
		err := copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir, source, "./missing-*.yaml")
		require.NoError(t, err)
	})

	t.Run("no matches without IgnoreMissingValueFiles returns an error", func(t *testing.T) {
		repoRoot, destDir, srcAppPath, destAppDir := setup(t, map[string]string{
			"values.yaml": "x",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir, source, "./missing-*.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched no files")
	})

	t.Run("no matches with nil Helm returns an error", func(t *testing.T) {
		repoRoot, destDir, srcAppPath, destAppDir := setup(t, map[string]string{
			"values.yaml": "x",
		})

		source := v1alpha1.ApplicationSource{}
		err := copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir, source, "./missing-*.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched no files")
	})

	t.Run("preserves nested directory layout", func(t *testing.T) {
		repoRoot, destDir, srcAppPath, destAppDir := setup(t, map[string]string{
			"envs/dev/values.yaml":     "dev",
			"envs/staging/values.yaml": "staging",
			"envs/dev/skip.txt":        "skip",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir, source, "./envs/*/values.yaml")
		require.NoError(t, err)

		assertFileContent(t, filepath.Join(destAppDir, "envs/dev/values.yaml"), "dev")
		assertFileContent(t, filepath.Join(destAppDir, "envs/staging/values.yaml"), "staging")
		assertFileMissing(t, filepath.Join(destAppDir, "envs/dev/skip.txt"))
	})

	t.Run("absolute valueFile is rejected", func(t *testing.T) {
		repoRoot, destDir, srcAppPath, destAppDir := setup(t, map[string]string{
			"values.yaml": "x",
		})

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{IgnoreMissingValueFiles: true},
		}
		err := copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir, source, "/etc/passwd")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "absolute value file paths are not permitted")
	})

	t.Run("path traversal escaping repo root is rejected", func(t *testing.T) {
		// Create a sensitive file outside the repo root, and a glob that
		// would match it via ../ traversal.
		repoRoot, destDir, srcAppPath, destAppDir := setup(t, map[string]string{
			"values.yaml": "x",
		})
		outside := filepath.Join(filepath.Dir(repoRoot), "secrets")
		require.NoError(t, os.MkdirAll(outside, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "password.yaml"), []byte("secret"), 0o600))

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir, source, "../../secrets/*.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes repo root")
	})

	t.Run("symlinked match escaping repo root is rejected", func(t *testing.T) {
		// Plant a symlink inside the repo that points to a file outside it.
		repoRoot, destDir, srcAppPath, destAppDir := setup(t, map[string]string{
			"values.yaml": "x",
		})
		outside := filepath.Join(filepath.Dir(repoRoot), "outside")
		require.NoError(t, os.MkdirAll(outside, 0o755))
		target := filepath.Join(outside, "evil.yaml")
		require.NoError(t, os.WriteFile(target, []byte("evil"), 0o600))
		linkPath := filepath.Join(srcAppPath, "values-evil.yaml")
		require.NoError(t, os.Symlink(target, linkPath))

		source := v1alpha1.ApplicationSource{
			Helm: &v1alpha1.ApplicationSourceHelm{},
		}
		err := copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir, source, "./values-*.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes repo root")
	})
}

func TestAbsClean(t *testing.T) {
	t.Run("cleans relative segments without resolving symlinks", func(t *testing.T) {
		root := t.TempDir()
		got, err := absClean(filepath.Join(root, "does", "..", "not", "exist"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(root, "not", "exist"), got)
	})
}

func TestResolveExisting(t *testing.T) {
	t.Run("evaluates symlinks for existing files", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "real.txt")
		require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))
		link := filepath.Join(root, "link.txt")
		require.NoError(t, os.Symlink(target, link))

		got, err := resolveExisting(link)
		require.NoError(t, err)
		// EvalSymlinks may resolve /var → /private/var on macOS; compare via
		// the same resolution applied to the target.
		expected, _ := filepath.EvalSymlinks(target)
		assert.Equal(t, expected, got)
	})

	t.Run("returns an error for non-existent paths", func(t *testing.T) {
		_, err := resolveExisting(filepath.Join(t.TempDir(), "missing"))
		require.Error(t, err)
	})
}

func TestIsWithin(t *testing.T) {
	cases := map[string]struct {
		child, parent string
		expected      bool
	}{
		"identical":      {"/a/b", "/a/b", true},
		"direct child":   {"/a/b/c", "/a/b", true},
		"deep child":     {"/a/b/c/d/e", "/a/b", true},
		"sibling":        {"/a/c", "/a/b", false},
		"parent":         {"/a", "/a/b", false},
		"prefix-but-not": {"/a/bc", "/a/b", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isWithin(tc.child, tc.parent))
		})
	}
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
