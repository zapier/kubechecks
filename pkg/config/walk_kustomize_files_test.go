package config

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKustomizeWalking(t *testing.T) {
	var (
		err error

		toBytes = func(s string) []byte {
			return []byte(s)
		}

		kustomizeApp1Name = "kustomize-app"
		kustomizeApp1Path = "test/app"

		kustomizeApp2Name = "kustomize-app-2"
		kustomizeApp2Path = "test/app2"

		fs = fstest.MapFS{
			"test/app/kustomization.yaml": {
				Data: toBytes(`
resources:
- file1.yaml
- ./file2.yaml
- ../file3.yaml
- ../overlays/base
- ./overlays/dev
- /common/overlays/prod
`)},

			"test/app2/kustomization.yaml": {
				Data: toBytes(`
resources:
- file1.yaml
- ../overlays/base
- /common/overlays/prod
`)},
			"test/overlays/base/kustomization.yaml": {
				Data: toBytes(`
resources:
- some-file1.yaml
- some-file2.yaml
- ../common
`)},

			"test/overlays/common/kustomization.yaml":  {Data: toBytes("hello: world")},
			"test/app/file1.yaml":                      {Data: toBytes("hello: world")},
			"test/app/file2.yaml":                      {Data: toBytes("hello: world")},
			"test/app2/file1.yaml":                     {Data: toBytes("hello: world")},
			"test/file3.yaml":                          {Data: toBytes("hello: world")},
			"test/app/overlays/dev/kustomization.yaml": {Data: toBytes("hello: world")},
			"common/overlays/prod/kustomization.yaml":  {Data: toBytes("hello: world")},
			"test/overlays/base/some-file1.yaml":       {Data: toBytes("hello: world")},
			"test/overlays/base/some-file2.yaml":       {Data: toBytes("hello: world")},
		}
	)

	appdir := NewAppDirectory()
	appdir.AddAppStub(kustomizeApp1Name, kustomizeApp1Path, false, true)
	appdir.AddAppStub(kustomizeApp2Name, kustomizeApp2Path, false, true)

	err = walkKustomizeFiles(appdir, fs, kustomizeApp1Name, kustomizeApp1Path)
	require.NoError(t, err)

	err = walkKustomizeFiles(appdir, fs, kustomizeApp2Name, kustomizeApp2Path)
	require.NoError(t, err)

	assert.Equal(t, map[string][]string{
		"test/app": {
			kustomizeApp1Name,
		},
		"test/app2": {
			kustomizeApp2Name,
		},
		"test/app/overlays/dev": {
			kustomizeApp1Name,
		},
		"test/overlays/base": {
			kustomizeApp1Name,
			kustomizeApp2Name,
		},
		"test/overlays/common": {
			kustomizeApp1Name,
			kustomizeApp2Name,
		},
		"common/overlays/prod": {
			kustomizeApp1Name,
			kustomizeApp2Name,
		},
	}, appdir.appDirs)

	assert.Equal(t, map[string][]string{
		"test/app/file1.yaml": {
			kustomizeApp1Name,
		},
		"test/app/file2.yaml": {
			kustomizeApp1Name,
		},
		"test/file3.yaml": {
			kustomizeApp1Name,
		},
		"test/overlays/base/some-file1.yaml": {
			kustomizeApp1Name,
			kustomizeApp2Name,
		},
		"test/overlays/base/some-file2.yaml": {
			kustomizeApp1Name,
			kustomizeApp2Name,
		},
		"test/app2/file1.yaml": {
			kustomizeApp2Name,
		},
	}, appdir.appFiles)
}
