package kustomize

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

type Processor interface {
	AddDir(string) error
	AddFile(string) error
}

func ProcessKustomizationFile(sourceFS fs.FS, relKustomizationPath string, processor Processor) error {
	dirName := filepath.Dir(relKustomizationPath)
	return processDir(sourceFS, dirName, processor)
}

func processDir(sourceFS fs.FS, relBase string, processor Processor) error {
	absKustPath := filepath.Join(relBase, "kustomization.yaml")

	// Parse using official Kustomization type
	file, err := sourceFS.Open(absKustPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No kustomization.yaml in this directory
		}
		return errors.Wrap(err, "failed to open file")
	}

	content, err := io.ReadAll(file)
	if err != nil {
		return errors.Wrap(err, "failed to read file")
	}

	var kust types.Kustomization
	if err := yaml.Unmarshal(content, &kust); err != nil {
		return errors.Wrap(err, "failed to parse kustomization.yaml")
	}

	// collect all the possible files and directories that kustomize can contain
	var filesOrDirectories []string
	filesOrDirectories = append(filesOrDirectories, kust.Bases...) // nolint:staticcheck // deprecated doesn't mean unused
	filesOrDirectories = append(filesOrDirectories, kust.Resources...)

	var directories []string
	directories = append(directories, kust.Components...)

	files := []string{"kustomization.yaml"}
	files = append(files, kust.Configurations...)
	files = append(files, kust.Crds...)
	files = append(files, kust.Transformers...)

	for _, patch := range kust.Patches {
		if patch.Path != "" {
			files = append(files, patch.Path)
		}
	}

	for _, patch := range kust.PatchesJson6902 { // nolint:staticcheck // deprecated doesn't mean unused
		if patch.Path != "" {
			files = append(files, patch.Path)
		}
	}

	for _, patch := range kust.PatchesStrategicMerge { // nolint:staticcheck // deprecated doesn't mean unused
		s := string(patch)
		if !strings.Contains(s, "\n") {
			files = append(files, s)
		}
	}

	// clean up the directories and files
	filesOrDirectories = cleanPaths(relBase, filesOrDirectories)
	directories = cleanPaths(relBase, directories)
	files = cleanPaths(relBase, files)

	// figure out if these are files or directories
	for _, fileOrDirectory := range filesOrDirectories {
		file, err := sourceFS.Open(fileOrDirectory)
		if err != nil {
			return errors.Wrapf(err, "failed to stat %s", fileOrDirectory)
		}

		if stat, err := file.Stat(); err != nil {
			return errors.Wrapf(err, "failed to stat %s", fileOrDirectory)
		} else if stat.IsDir() {
			directories = append(directories, fileOrDirectory)
		} else {
			files = append(files, fileOrDirectory)
		}
	}

	// add files
	for _, relResource := range files {
		if err := processor.AddFile(relResource); err != nil {
			return errors.Wrapf(err, "failed to add resource %q", relResource)
		}
	}

	// process directories and add them
	for _, relResource := range directories {
		if err = processor.AddDir(relResource); err != nil {
			return errors.Wrapf(err, "failed to add directory %q", relResource)
		}
		if err = processDir(sourceFS, relResource, processor); err != nil {
			return errors.Wrapf(err, "failed to process %q", relResource)
		}
	}

	return nil
}

func cleanPaths(relativeBase string, paths []string) []string {
	var result []string

	for _, path := range paths {
		if isRemoteResource(path) {
			continue
		}

		if strings.HasPrefix(path, "/") {
			path = strings.TrimPrefix(path, "/")
		} else {
			path = filepath.Join(relativeBase, path)
		}
		result = append(result, path)
	}

	return result
}

func isRemoteResource(resource string) bool {
	// Check for URL schemes
	if strings.Contains(resource, "://") {
		return true
	}

	// Check for common Git SSH patterns
	if strings.HasPrefix(resource, "git@") {
		return true
	}

	// Check for Kustomize's special GitHub/Bitbucket shorthand
	if strings.HasPrefix(resource, "github.com/") ||
		strings.HasPrefix(resource, "bitbucket.org/") ||
		strings.HasPrefix(resource, "gitlab.com/") {
		return true
	}

	// Check for HTTP(S) URLs without explicit scheme (kustomize allows this)
	if strings.HasPrefix(resource, "//") {
		return true
	}

	return false
}
