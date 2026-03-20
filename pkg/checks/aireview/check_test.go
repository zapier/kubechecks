package aireview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/git"
)

func TestBuildChangedFilesContent(t *testing.T) {
	tests := []struct {
		name         string
		setupRepo    func(t *testing.T) *git.Repo // nil means no repo
		changedFiles []string
		wantEmpty    bool
		wantContains []string
		wantMissing  []string
	}{
		{
			name:         "nil repo returns empty",
			setupRepo:    nil,
			changedFiles: []string{"a.yaml"},
			wantEmpty:    true,
		},
		{
			name:         "no changed files returns empty",
			setupRepo:    func(t *testing.T) *git.Repo { return &git.Repo{Directory: t.TempDir()} },
			changedFiles: nil,
			wantEmpty:    true,
		},
		{
			name: "reads valid file with line numbers",
			setupRepo: func(t *testing.T) *git.Repo {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("key: value\nfoo: bar"), 0644))
				return &git.Repo{Directory: dir}
			},
			changedFiles: []string{"values.yaml"},
			wantContains: []string{"## Changed Files", "### File: `values.yaml`", "   1 | key: value", "   2 | foo: bar"},
		},
		{
			name: "skips nonexistent files",
			setupRepo: func(t *testing.T) *git.Repo {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "exists.yaml"), []byte("hello"), 0644))
				return &git.Repo{Directory: dir}
			},
			changedFiles: []string{"missing.yaml", "exists.yaml"},
			wantContains: []string{"exists.yaml"},
			wantMissing:  []string{"missing.yaml"},
		},
		{
			name: "blocks path traversal with relative path",
			setupRepo: func(t *testing.T) *git.Repo {
				dir := t.TempDir()
				outsideDir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("sensitive"), 0644))
				// Store the traversal path in a temp file so we can retrieve it
				relPath, err := filepath.Rel(dir, filepath.Join(outsideDir, "secret.txt"))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".relpath"), []byte(relPath), 0644))
				return &git.Repo{Directory: dir}
			},
			changedFiles: nil, // set dynamically below
			wantEmpty:    true,
		},
		{
			name: "blocks path traversal with dot-dot prefix",
			setupRepo: func(t *testing.T) *git.Repo {
				return &git.Repo{Directory: t.TempDir()}
			},
			changedFiles: []string{"../../../etc/passwd"},
			wantEmpty:    true,
		},
		{
			name: "multiple valid files",
			setupRepo: func(t *testing.T) *git.Repo {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("a: 1"), 0644))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "b.yaml"), []byte("b: 2"), 0644))
				return &git.Repo{Directory: dir}
			},
			changedFiles: []string{"a.yaml", "b.yaml"},
			wantContains: []string{"### File: `a.yaml`", "### File: `b.yaml`"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var repo *git.Repo
			if tt.setupRepo != nil {
				repo = tt.setupRepo(t)
			}

			changedFiles := tt.changedFiles
			// Handle the relative path traversal case dynamically
			if tt.name == "blocks path traversal with relative path" {
				relPathData, err := os.ReadFile(filepath.Join(repo.Directory, ".relpath"))
				require.NoError(t, err)
				changedFiles = []string{string(relPathData)}
			}

			req := checks.Request{
				Repo:         repo,
				ChangedFiles: changedFiles,
			}
			result := buildChangedFilesContent(req)

			if tt.wantEmpty {
				assert.Empty(t, result)
				return
			}
			for _, s := range tt.wantContains {
				assert.Contains(t, result, s)
			}
			for _, s := range tt.wantMissing {
				assert.NotContains(t, result, s)
			}
		})
	}
}
