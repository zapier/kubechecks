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
		files, dirs, err := processDir(sourceFS, "testdir")
		assert.NoError(t, err)
		assert.Empty(t, files)
		assert.Empty(t, dirs)
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
		_, _, err := processDir(mfs, "testdir")
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
		_, _, err := processDir(mfs, "testdir")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read file")
	})

	t.Run("ValidKustomization", func(t *testing.T) {
		kustContent := `
resources:
 - resource.yaml
bases:
 - base
components:
 - components
`
		sourceFS := fstest.MapFS{
			"testdir/kustomization.yaml": &fstest.MapFile{
				Data: []byte(kustContent),
			},
			"testdir/resource.yaml": &fstest.MapFile{},
			"testdir/base/kustomization.yaml": &fstest.MapFile{
				Data: []byte(`resources: ["base_resource.yaml"]`),
			},
			"testdir/base/base_resource.yaml": &fstest.MapFile{},
			"testdir/components/kustomization.yaml": &fstest.MapFile{
				Data: []byte(``), // Empty valid kustomization
			},
		}

		files, dir, err := processDir(sourceFS, "testdir")
		require.NoError(t, err)

		expectedFiles := []string{
			"testdir/kustomization.yaml",
			"testdir/resource.yaml",
			"testdir/base/kustomization.yaml",
			"testdir/base/base_resource.yaml",
			"testdir/components/kustomization.yaml",
		}
		sort.Strings(files)
		sort.Strings(expectedFiles)
		assert.Equal(t, expectedFiles, files)

		expectedDirs := []string{
			"testdir",
			"testdir/base",
			"testdir/components",
		}
		sort.Strings(dir)
		sort.Strings(expectedDirs)
		assert.Equal(t, expectedDirs, dir)
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

		files, _, err := processDir(sourceFS, "testdir")
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

		files, _, err := processDir(sourceFS, "testdir")
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

		files, _, err := processDir(sourceFS, "testdir")
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

		_, _, err := processDir(sourceFS, "testdir")
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
    namespace: dumy
    includeCRDs: true
    valuesFile: values-dummy.yaml
`
		valueContent := `
dummy:
  lables:
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

		files, _, err := processDir(sourceFS, "testdir")
		assert.NoError(t, err)
		assert.Contains(t, files, "testdir/values-dummy.yaml")
	})
}
