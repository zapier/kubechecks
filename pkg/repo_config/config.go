package repo_config

import (
	"github.com/creasty/defaults"
	"github.com/pkg/errors"
)

type Config struct {
	Applications    []*ArgoCdApplicationConfig    `yaml:"applications"`
	ApplicationSets []*ArgocdApplicationSetConfig `yaml:"applicationSets"`
}

type ArgoCdApplicationConfig struct {
	Name              string   `validate:"empty=false" yaml:"name"`
	Cluster           string   `validate:"empty=false" yaml:"cluster"`
	Path              string   `validate:"empty=false" yaml:"path"`
	AdditionalPaths   []string `yaml:"additionalPaths"`
	EnableConfTest    bool     `default:"true"         yaml:"enableConfTest"`
	EnableKubeConform bool     `default:"true"         yaml:"enableKubeConform"`
	EnableKubePug     bool     `default:"true"         yaml:"enableKubePug"`
}

func (s *ArgoCdApplicationConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := defaults.Set(s)
	if err != nil {
		return errors.Wrap(err, "failed to set defaults for project config")
	}

	type plain ArgoCdApplicationConfig
	if err := unmarshal((*plain)(s)); err != nil {
		return err
	}

	return nil
}

type ArgocdApplicationSetConfig struct {
	Name              string   `validate:"empty=false" yaml:"name"`
	Paths             []string `validate:"empty=false" yaml:"paths"`
	EnableConfTest    bool     `default:"true"         yaml:"enableConfTest"`
	EnableKubeConform bool     `default:"true"         yaml:"enableKubeConform"`
	EnableKubePug     bool     `default:"true"         yaml:"enableKubePug"`
}

func (s *ArgocdApplicationSetConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := defaults.Set(s)
	if err != nil {
		return errors.Wrap(err, "failed to set defaults for project config")
	}

	type plain ArgocdApplicationSetConfig
	if err := unmarshal((*plain)(s)); err != nil {
		return err
	}

	return nil
}
