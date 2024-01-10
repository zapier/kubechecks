package config

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/util/yaml"
)

type patchJson6902 struct {
	Path string `yaml:"path"`
}

func walkKustomizeFiles(result *AppDirectory, fs fs.FS, appName, dirpath string) error {
	kustomizeFile := filepath.Join(dirpath, "kustomization.yaml")

	var (
		err error

		kustomize struct {
			Bases                 []string        `yaml:"bases"`
			Resources             []string        `yaml:"resources"`
			PatchesJson6902       []patchJson6902 `yaml:"patchesJson6902"`
			PatchesStrategicMerge []string        `yaml:"patchesStrategicMerge"`
		}
	)

	reader, err := fs.Open(kustomizeFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return errors.Wrap(err, "failed to open file")
	}

	bytes, err := io.ReadAll(reader)
	if err != nil {
		return errors.Wrap(err, "failed to read file")
	}

	if err = yaml.Unmarshal(bytes, &kustomize); err != nil {
		return errors.Wrap(err, "failed to unmarshal file")
	}

	for _, resource := range kustomize.Resources {
		if strings.Contains(resource, "://") {
			// no reason to walk remote files, since they can't be changed
			continue
		}

		var relPath string
		if len(resource) >= 1 && resource[0] == '/' {
			relPath = resource[1:]
		} else {
			relPath = filepath.Join(dirpath, resource)
		}

		file, err := fs.Open(relPath)
		if err != nil {
			return errors.Wrapf(err, "failed to read %s", relPath)
		}
		stat, err := file.Stat()
		if err != nil {
			log.Warn().Err(err).Msgf("failed to stat %s", relPath)
		}

		if !stat.IsDir() {
			result.AddFile(appName, relPath)
			continue
		}

		result.AddDir(appName, relPath)
		if err = walkKustomizeFiles(result, fs, appName, relPath); err != nil {
			log.Warn().Err(err).Msgf("failed to read kustomize.yaml in %s", relPath)
		}
	}

	for _, basePath := range kustomize.Bases {
		relPath := filepath.Join(dirpath, basePath)
		result.AddDir(appName, relPath)
		if err = walkKustomizeFiles(result, fs, appName, relPath); err != nil {
			log.Warn().Err(err).Msgf("failed to read kustomize.yaml in %s", relPath)
		}
	}

	for _, patchFile := range kustomize.PatchesStrategicMerge {
		relPath := filepath.Join(dirpath, patchFile)
		result.AddFile(appName, relPath)
	}

	for _, patch := range kustomize.PatchesJson6902 {
		relPath := filepath.Join(dirpath, patch.Path)
		result.AddFile(appName, relPath)
	}

	return nil
}
