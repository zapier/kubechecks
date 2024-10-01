package repo_config

import (
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"gopkg.in/dealancer/validate.v2"
	"gopkg.in/yaml.v3"
)

const RepoConfigFilenamePrefix = `.kubechecks`

var RepoConfigFileExtensions = []string{".yaml", ".yml"}

var ErrConfigFileNotFound = errors.New("project config file not found")

// LoadRepoConfig attempts to load a config file from the given directory
// it searches the dir for all the config file name variations.
func LoadRepoConfig(repoDir string) (*Config, error) {
	file, err := searchConfigFile(repoDir)
	if err != nil {
		if errors.Is(err, ErrConfigFileNotFound) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "failed to find config file")
	}

	cfg, err := loadRepoConfigFile(file)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load repo config file")
	}

	return cfg, nil
}

func RepoConfigFilenameVariations() []string {
	var filenames []string
	for _, ext := range RepoConfigFileExtensions {
		filenames = append(filenames, RepoConfigFilenamePrefix+ext)
	}
	return filenames
}

func searchConfigFile(repoDir string) (string, error) {
	for _, ext := range RepoConfigFileExtensions {
		fn := filepath.Join(repoDir, RepoConfigFilenamePrefix+ext)
		fi, err := os.Stat(fn)
		if err != nil && !os.IsNotExist(err) {
			log.Warn().Err(err).Str("filename", fn).Msg("error while attempting to read project config file")
			continue
		}
		if fi != nil && !fi.IsDir() {
			return fn, nil
		}
	}

	return "", ErrConfigFileNotFound
}

func loadRepoConfigFile(file string) (*Config, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		log.Error().Err(err).Str("filename", file).Msg("could not read project config file")
	}
	return LoadRepoConfigBytes(b)
}

func LoadRepoConfigBytes(b []byte) (*Config, error) {
	cfg := &Config{}
	err := yaml.Unmarshal(b, cfg)
	if err != nil {
		return nil, errors.Wrap(err, "could not parse Project config file (.kubechecks.yaml)")
	}

	if err := validate.Validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
