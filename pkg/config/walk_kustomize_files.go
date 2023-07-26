package config

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func walkKustomizeFiles(result *AppDirectory, fs fs.FS, appName, dirpath string) error {
	kustomizeFile := filepath.Join(dirpath, "kustomization.yaml")

	var (
		err error

		kustomize struct {
			Resources []string `yaml:"resources"`

			PatchesStrategicMerge []string `yaml:"patchesStrategicMerge"`
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
		var relPath string
		if len(resource) >= 1 && resource[0] == '/' {
			relPath = resource[1:]
		} else {
			relPath = filepath.Join(dirpath, resource)
		}

		file, err := fs.Open(relPath)
		if err != nil {
			log.Warn().Err(err).Msgf("failed to read %s", relPath)
			continue
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

	for _, patchFile := range kustomize.PatchesStrategicMerge {
		relPath := filepath.Join(dirpath, patchFile)
		result.AddFile(appName, relPath)
	}

	return nil
}
