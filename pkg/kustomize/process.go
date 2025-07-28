package kustomize

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

var ErrUnexpectedFilename = errors.New("kustomization file must be called kustomization.yaml")

// ProcessKustomizationFile processes a kustomization file and returns all the files and directories it references.
func ProcessKustomizationFile(sourceFS fs.FS, relKustomizationPath string) (files, dirs []string, err error) {
	filename := filepath.Base(relKustomizationPath)
	if filename != "kustomization.yaml" {
		return nil, nil, fmt.Errorf("%q was unexpected: %w", relKustomizationPath, ErrUnexpectedFilename)
	}

	dirName := filepath.Dir(relKustomizationPath)

	proc := processor{
		visitedDirs: make(map[string]struct{}),
	}

	files, dirs, err = proc.processDir(sourceFS, dirName)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to process kustomize file %q", relKustomizationPath)
	}

	return files, dirs, nil
}

type processor struct {
	visitedDirs map[string]struct{}
}

func (p processor) processDir(sourceFS fs.FS, relBase string) (files, dirs []string, err error) {
	if _, ok := p.visitedDirs[relBase]; ok {
		log.Warn().Msgf("directory %q already processed", relBase)
		return nil, nil, nil
	}

	log.Debug().Msgf("processing directory %q", relBase)
	p.visitedDirs[relBase] = struct{}{}

	absKustPath := filepath.Join(relBase, "kustomization.yaml")

	// Parse using official Kustomization type
	file, err := sourceFS.Open(absKustPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, []string{relBase}, nil // No kustomization.yaml in this directory, the dir is the important thing
		}

		return nil, nil, errors.Wrapf(err, "failed to open file %q", absKustPath)
	}

	content, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to read file kustomization.yam")
	}

	var kust types.Kustomization
	if err := yaml.Unmarshal(content, &kust); err != nil {
		return nil, nil, errors.Wrapf(err, "failed to parse %q", absKustPath)
	}

	// collect all the possible files and directories that kustomize can contain
	var filesOrDirectories []string
	filesOrDirectories = append(filesOrDirectories, kust.Bases...) // nolint:staticcheck // deprecated doesn't mean unused
	filesOrDirectories = append(filesOrDirectories, kust.Resources...)

	var directories []string
	directories = append(directories, kust.Components...)

	files = []string{"kustomization.yaml"}
	files = append(files, kust.Configurations...)
	files = append(files, kust.Crds...)
	files = append(files, kust.Transformers...)

	for _, helm := range kust.HelmCharts {
		files = append(files, helm.ValuesFile)
	}

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

	for _, configmapGenerator := range kust.ConfigMapGenerator {
		if configmapGenerator.EnvSource != "" {
			files = append(files, configmapGenerator.EnvSource)
		}
		files = append(files, configmapGenerator.EnvSources...)
		files = append(files, extractFileSourcePaths(configmapGenerator.FileSources)...)
	}

	for _, secretGenerator := range kust.SecretGenerator {
		if secretGenerator.EnvSource != "" {
			files = append(files, secretGenerator.EnvSource)
		}
		files = append(files, secretGenerator.EnvSources...)
		files = append(files, extractFileSourcePaths(secretGenerator.FileSources)...)
	}

	// clean up the directories and files
	filesOrDirectories = cleanPaths(relBase, filesOrDirectories)
	directories = cleanPaths(relBase, directories)
	files = cleanPaths(relBase, files)

	// figure out if these are files or directories
	for _, fileOrDirectory := range filesOrDirectories {
		file, err := sourceFS.Open(fileOrDirectory)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to stat %s", fileOrDirectory)
		}

		if stat, err := file.Stat(); err != nil {
			return nil, nil, errors.Wrapf(err, "failed to stat %s", fileOrDirectory)
		} else if stat.IsDir() {
			directories = append(directories, fileOrDirectory)
		} else {
			files = append(files, fileOrDirectory)
		}
	}

	allFiles := append([]string(nil), files...)
	var allDirs []string

	// process directories and add them
	for _, relResource := range directories {
		subFiles, subDirs, err := p.processDir(sourceFS, relResource)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to process %q", relResource)
		}
		allFiles = append(allFiles, subFiles...)
		allDirs = append(allDirs, subDirs...)
	}

	return allFiles, allDirs, nil
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

func extractFileSourcePaths(fileSources []string) []string {
	var files []string
	for _, fileSource := range fileSources {
		if strings.Contains(fileSource, "=") {
			slicedFileSource := strings.SplitN(fileSource, "=", 2)
			if slicedFileSource[1] == "" {
				log.Warn().Msgf("invalid file source %q, expected format {key}=path", fileSource)
				continue
			}
			files = append(files, slicedFileSource[1])
			continue
		}
		files = append(files, fileSource)
	}
	return files
}
