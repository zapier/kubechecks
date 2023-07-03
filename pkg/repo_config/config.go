package repo_config

import (
	"fmt"

	"github.com/creasty/defaults"
)

type Config struct {
	Applications    []*ArgoCdApplicationConfig    `yaml:"applications"`
	ApplicationSets []*ArgocdApplicationSetConfig `yaml:"applicationSets"`
}

type ArgoCdApplicationConfig struct {
	Name              string   `yaml:"name" validate:"empty=false"`
	Cluster           string   `yaml:"cluster" validate:"empty=false"`
	Path              string   `yaml:"path" validate:"empty=false"`
	AdditionalPaths   []string `yaml:"additionalPaths"`
	EnableConfTest    bool     `yaml:"enableConfTest" default:"true"`
	EnableKubeConform bool     `yaml:"enableKubeConform" default:"true"`
	EnableKubePug     bool     `yaml:"enableKubePug" default:"true"`
}

func (s *ArgoCdApplicationConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := defaults.Set(s)
	if err != nil {
		return fmt.Errorf("failed to set defaults for project config: %v", err)
	}

	type plain ArgoCdApplicationConfig
	if err := unmarshal((*plain)(s)); err != nil {
		return err
	}

	return nil
}

type ArgocdApplicationSetConfig struct {
	Name              string   `yaml:"name" validate:"empty=false"`
	Paths             []string `yaml:"paths" validate:"empty=false"`
	EnableConfTest    bool     `yaml:"enableConfTest" default:"true"`
	EnableKubeConform bool     `yaml:"enableKubeConform" default:"true"`
	EnableKubePug     bool     `yaml:"enableKubePug" default:"true"`
}

func (s *ArgocdApplicationSetConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := defaults.Set(s)
	if err != nil {
		return fmt.Errorf("failed to set defaults for project config: %v", err)
	}

	type plain ArgocdApplicationSetConfig
	if err := unmarshal((*plain)(s)); err != nil {
		return err
	}

	return nil
}
