package kustomize

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "sigs.k8s.io/kustomize/api/types"
)

// mockFS is a custom filesystem mock to simulate errors.
type mockFS struct {
	openFunc func(name string) (fs.File, error)
}

func (m *mockFS) Open(name string) (fs.File, error) {
	return m.openFunc(name)
}

// errorReader is a fs.File that returns an error on Read.
type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read error")
}

func (e *errorReader) Close() error {
	return nil
}

func (e *errorReader) Stat() (fs.FileInfo, error) {
	return dummyFileInfo{name: "kustomization.yaml", isDir: false}, nil
}

// dummyFileInfo implements fs.FileInfo for mocking.
type dummyFileInfo struct {
	name  string
	isDir bool
}

func (d dummyFileInfo) Name() string       { return d.name }
func (d dummyFileInfo) Size() int64        { return 0 }
func (d dummyFileInfo) Mode() fs.FileMode  { return 0 }
func (d dummyFileInfo) ModTime() time.Time { return time.Time{} }
func (d dummyFileInfo) IsDir() bool        { return d.isDir }
func (d dummyFileInfo) Sys() interface{}   { return nil }

func TestProcessDir(t *testing.T) {
	t.Run("NoKustomization", func(t *testing.T) {
		sourceFS := fstest.MapFS{}
		files, dirs, err := ProcessKustomizationFile(sourceFS, filepath.Join("testdir", "kustomization.yaml"))
		assert.NoError(t, err)
		assert.Empty(t, files)
		assert.Equal(t, []string{"testdir"}, dirs)
	})

	t.Run("OpenError", func(t *testing.T) {
		mfs := &mockFS{
			openFunc: func(name string) (fs.File, error) {
				if name == filepath.Join("testdir", "kustomization.yaml") {
					return nil, os.ErrPermission
				}
				return nil, fs.ErrNotExist
			},
		}
		_, _, err := ProcessKustomizationFile(mfs, filepath.Join("testdir", "kustomization.yaml"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to open file")
	})

	t.Run("ReadError", func(t *testing.T) {
		mfs := &mockFS{
			openFunc: func(name string) (fs.File, error) {
				if name == filepath.Join("testdir", "kustomization.yaml") {
					return &errorReader{}, nil
				}
				return nil, fs.ErrNotExist
			},
		}
		_, _, err := ProcessKustomizationFile(mfs, filepath.Join("testdir", "kustomization.yaml"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read file")
	})

	t.Run("ValidKustomization", func(t *testing.T) {
		kustContent := `
resources:
- resource.yaml
- ../rootdir
bases:
- base
components:
- components
`
		sourceFS := fstest.MapFS{
			"testdir/kustomization.yaml": &fstest.MapFile{
				Data: []byte(kustContent),
			},
			"rootdir/file.yaml":     &fstest.MapFile{},
			"testdir/resource.yaml": &fstest.MapFile{},
			"testdir/base/kustomization.yaml": &fstest.MapFile{
				Data: []byte(`resources: ["base_resource.yaml"]`),
			},
			"testdir/base/base_resource.yaml": &fstest.MapFile{},
			"testdir/components/kustomization.yaml": &fstest.MapFile{
				Data: []byte(``), // Empty valid kustomization
			},
		}

		files, dirs, err := ProcessKustomizationFile(sourceFS, filepath.Join("testdir", "kustomization.yaml"))
		require.NoError(t, err)

		expectedFiles := []string{
			"testdir/kustomization.yaml",
			"testdir/resource.yaml",
			"testdir/base/kustomization.yaml",
			"testdir/base/base_resource.yaml",
			"testdir/components/kustomization.yaml",
		}
		expectedDirs := []string{
			"rootdir",
		}
		sort.Strings(files)
		sort.Strings(expectedFiles)
		assert.Equal(t, expectedFiles, files)
		assert.Equal(t, expectedDirs, dirs)
	})

	t.Run("relative components", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"apps/app1/overlays/env1/kustomization.yaml": &fstest.MapFile{
				Data: []byte(`
components:
  - ../../components/component1
  - ../../components/component2
`),
			},
			"apps/app1/components/component1/kustomization.yaml": &fstest.MapFile{
				Data: []byte(`
resources:
- resource1.yaml`),
			},
			"apps/app1/components/component1/resource1.yaml": &fstest.MapFile{},
			"apps/app1/components/component2/resource1.yaml": &fstest.MapFile{},
			"apps/app1/components/component2/resource2.yaml": &fstest.MapFile{},
		}

		files, dirs, err := ProcessKustomizationFile(sourceFS, filepath.Join("apps", "app1", "overlays", "env1", "kustomization.yaml"))
		require.NoError(t, err)
		assert.Equal(t, []string{
			"apps/app1/overlays/env1/kustomization.yaml",
			"apps/app1/components/component1/kustomization.yaml",
			"apps/app1/components/component1/resource1.yaml",
		}, files)
		assert.Equal(t, []string{
			"apps/app1/components/component2",
		}, dirs)
	})

	t.Run("StrategicMergePatch", func(t *testing.T) {
		kustContent := `
patchesStrategicMerge:
 - |-
   apiVersion: apps/v1
   kind: Deployment
`
		sourceFS := fstest.MapFS{
			"testdir/kustomization.yaml": &fstest.MapFile{
				Data: []byte(kustContent),
			},
		}

		files, _, err := ProcessKustomizationFile(sourceFS, filepath.Join("testdir", "kustomization.yaml"))
		assert.NoError(t, err)
		assert.Equal(t, []string{"testdir/kustomization.yaml"}, files)
	})

	t.Run("Patch", func(t *testing.T) {
		kustContent := `
patches:
 - path: patch.yaml
`
		sourceFS := fstest.MapFS{
			"testdir/kustomization.yaml": &fstest.MapFile{
				Data: []byte(kustContent),
			},
			"testdir/patch.yaml": &fstest.MapFile{},
		}

		files, _, err := ProcessKustomizationFile(sourceFS, filepath.Join("testdir", "kustomization.yaml"))
		require.NoError(t, err)
		assert.Contains(t, files, "testdir/patch.yaml")
	})

	t.Run("PatchesJson6902", func(t *testing.T) {
		kustContent := `
patchesJson6902:
 - path: patch.yaml
`
		sourceFS := fstest.MapFS{
			"testdir/kustomization.yaml": &fstest.MapFile{
				Data: []byte(kustContent),
			},
			"testdir/patch.yaml": &fstest.MapFile{},
		}

		files, _, err := ProcessKustomizationFile(sourceFS, filepath.Join("testdir", "kustomization.yaml"))
		require.NoError(t, err)
		assert.Contains(t, files, "testdir/patch.yaml")
	})

	t.Run("StatError", func(t *testing.T) {
		kustContent := `
resources:
 - missing-resource.yaml
`
		sourceFS := fstest.MapFS{
			"testdir/kustomization.yaml": &fstest.MapFile{
				Data: []byte(kustContent),
			},
		}

		_, _, err := ProcessKustomizationFile(sourceFS, filepath.Join("testdir", "kustomization.yaml"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to stat testdir/missing-resource.yaml")
	})

	t.Run("helmChart", func(t *testing.T) {
		kustContent := `
helmCharts:
  - name: dummy
    repo: https://dummy.local/repo
    version: 1.2.3
    releaseName: dummy
    namespace: dummy
    includeCRDs: true
    valuesFile: values-dummy.yaml
`
		valueContent := `
dummy:
  labels:
    release: dummy
`
		sourceFS := fstest.MapFS{
			"testdir/kustomization.yaml": &fstest.MapFile{
				Data: []byte(kustContent),
			},
			"testdir/values-dummy.yaml": &fstest.MapFile{
				Data: []byte(valueContent),
			},
		}

		files, _, err := ProcessKustomizationFile(sourceFS, filepath.Join("testdir", "kustomization.yaml"))
		assert.NoError(t, err)
		assert.Contains(t, files, "testdir/values-dummy.yaml")
	})
}

func TestIsRemoteResource(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		want     bool
	}{
		{
			name:     "http url",
			resource: "http://example.com/path",
			want:     true,
		},
		{
			name:     "https url",
			resource: "https://example.com/path",
			want:     true,
		},
		{
			name:     "git ssh url",
			resource: "git@github.com:user/repo.git",
			want:     true,
		},
		{
			name:     "github shorthand",
			resource: "github.com/user/repo",
			want:     true,
		},
		{
			name:     "bitbucket shorthand",
			resource: "bitbucket.org/user/repo",
			want:     true,
		},
		{
			name:     "gitlab shorthand",
			resource: "gitlab.com/user/repo",
			want:     true,
		},
		{
			name:     "url without scheme",
			resource: "//example.com/path",
			want:     true,
		},
		{
			name:     "local path",
			resource: "./path/to/resource",
			want:     false,
		},
		{
			name:     "absolute path",
			resource: "/path/to/resource",
			want:     false,
		},
		{
			name:     "relative path",
			resource: "../path/to/resource",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRemoteResource(tt.resource)
			assert.Equal(t, tt.want, got)
		})
	}
}
