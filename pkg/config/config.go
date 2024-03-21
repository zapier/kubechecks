package config

import (
	"reflect"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/zapier/kubechecks/pkg"
)

type ServerConfig struct {
	// argocd
	ArgoCDServerAddr string `mapstructure:"argocd-api-server-addr"`
	ArgoCDToken      string `mapstructure:"argocd-api-token"`
	ArgoCDPathPrefix string `mapstructure:"argocd-api-path-prefix"`
	ArgoCDInsecure   bool   `mapstructure:"argocd-api-insecure"`
	KubernetesConfig string `mapstructure:"kubernetes-config"`

	// otel
	EnableOtel        bool   `mapstructure:"otel-enabled"`
	OtelCollectorHost string `mapstructure:"otel-collector-host"`
	OtelCollectorPort string `mapstructure:"otel-collector-port"`

	// vcs
	VcsBaseUrl string `mapstructure:"vcs-base-url"`
	VcsToken   string `mapstructure:"vcs-token"`
	VcsType    string `mapstructure:"vcs-type"`

	// webhooks
	EnsureWebhooks bool   `mapstructure:"ensure-webhooks"`
	WebhookSecret  string `mapstructure:"webhook-secret"`
	WebhookUrlBase string `mapstructure:"webhook-url-base"`
	UrlPrefix      string `mapstructure:"webhook-url-prefix"`

	// checks
	// -- conftest
	EnableConfTest     bool            `mapstructure:"enable-conftest"`
	PoliciesLocation   []string        `mapstructure:"policies-location"`
	WorstConfTestState pkg.CommitState `mapstructure:"worst-conftest-state"`
	// -- kubeconform
	EnableKubeConform     bool            `mapstructure:"enable-kubeconform"`
	WorstKubeConformState pkg.CommitState `mapstructure:"worst-kubeconform-state"`
	// -- preupgrade
	EnablePreupgrade     bool            `mapstructure:"enable-preupgrade"`
	WorstPreupgradeState pkg.CommitState `mapstructure:"worst-preupgrade-state"`

	// misc
	FallbackK8sVersion       string        `mapstructure:"fallback-k8s-version"`
	LabelFilter              string        `mapstructure:"label-filter"`
	LogLevel                 zerolog.Level `mapstructure:"log-level"`
	MonitorAllApplications   bool          `mapstructure:"monitor-all-applications"`
	OpenAIAPIToken           string        `mapstructure:"openai-api-token"`
	RepoRefreshInterval      time.Duration `mapstructure:"repo-refresh-interval"`
	SchemasLocations         []string      `mapstructure:"schemas-location"`
	ShowDebugInfo            bool          `mapstructure:"show-debug-info"`
	TidyOutdatedCommentsMode string        `mapstructure:"tidy-outdated-comments-mode"`
}

func New() (ServerConfig, error) {
	return NewWithViper(viper.GetViper())
}

func NewWithViper(v *viper.Viper) (ServerConfig, error) {
	var cfg ServerConfig
	if err := v.Unmarshal(&cfg, func(config *mapstructure.DecoderConfig) {
		config.DecodeHook = func(in reflect.Type, out reflect.Type, value interface{}) (interface{}, error) {
			if in.String() == "string" && out.String() == "zerolog.Level" {
				input := value.(string)
				return zerolog.ParseLevel(input)
			}

			if in.String() == "string" && out.String() == "pkg.CommitState" {
				input := value.(string)
				return pkg.ParseCommitState(input)
			}

			if in.String() == "string" && out.String() == "time.Duration" {
				input := value.(string)
				return time.ParseDuration(input)
			}

			return value, nil
		}
	}); err != nil {
		return cfg, errors.Wrap(err, "failed to read configuration")
	}

	log.Info().Msg("Server Configuration: ")
	log.Info().Msgf("Webhook URL Base: %s", cfg.WebhookUrlBase)
	log.Info().Msgf("Webhook URL Prefix: %s", cfg.UrlPrefix)
	log.Info().Msgf("VCS Type: %s", cfg.VcsType)

	return cfg, nil
}
