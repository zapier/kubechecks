package config

import (
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/spf13/viper"

	"github.com/zapier/kubechecks/pkg"
)

// set default values in /cmd/root.go's init function

type ServerConfig struct {
	// argocd
	ArgoCDServerAddr    string `mapstructure:"argocd-api-server-addr"`
	ArgoCDToken         string `mapstructure:"argocd-api-token"`
	ArgoCDPathPrefix    string `mapstructure:"argocd-api-path-prefix"`
	ArgoCDInsecure      bool   `mapstructure:"argocd-api-insecure"`
	ArgoCDNamespace     string `mapstructure:"argocd-api-namespace"`
	ArgoCDPlainText     bool   `mapstructure:"argocd-api-plaintext"`
	KubernetesConfig    string `mapstructure:"kubernetes-config"`
	KubernetesType      string `mapstructure:"kubernetes-type"`
	KubernetesClusterID string `mapstructure:"kubernetes-clusterid"`

	// otel
	EnableOtel        bool   `mapstructure:"otel-enabled"`
	OtelCollectorHost string `mapstructure:"otel-collector-host"`
	OtelCollectorPort string `mapstructure:"otel-collector-port"`

	// vcs
	VcsBaseUrl   string `mapstructure:"vcs-base-url"`
	VcsUploadUrl string `mapstructure:"vcs-upload-url"` // github enterprise upload URL
	VcsToken     string `mapstructure:"vcs-token"`
	VcsType      string `mapstructure:"vcs-type"`

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
	// -- hooks
	EnableHooksRenderer bool            `mapstructure:"enable-hooks-renderer"`
	WorstHooksState     pkg.CommitState `mapstructure:"worst-hooks-state"`
	// -- kubeconform
	EnableKubeConform     bool            `mapstructure:"enable-kubeconform"`
	WorstKubeConformState pkg.CommitState `mapstructure:"worst-kubeconform-state"`
	// -- preupgrade
	EnablePreupgrade     bool            `mapstructure:"enable-preupgrade"`
	WorstPreupgradeState pkg.CommitState `mapstructure:"worst-preupgrade-state"`

	// misc
	FallbackK8sVersion       string        `mapstructure:"fallback-k8s-version"`
	LabelFilter              string        `mapstructure:"label-filter"`
	LogLevel                 slog.Level    `mapstructure:"log-level"`
	MonitorAllApplications   bool          `mapstructure:"monitor-all-applications"`
	OpenAIAPIToken           string        `mapstructure:"openai-api-token"`
	RepoRefreshInterval      time.Duration `mapstructure:"repo-refresh-interval"`
	SchemasLocations         []string      `mapstructure:"schemas-location"`
	ShowDebugInfo            bool          `mapstructure:"show-debug-info"`
	TidyOutdatedCommentsMode string        `mapstructure:"tidy-outdated-comments-mode"`
	MaxQueueSize             int64         `mapstructure:"max-queue-size"`
	MaxConcurrenctChecks     int           `mapstructure:"max-concurrenct-checks"`
}

func New() (ServerConfig, error) {
	return NewWithViper(viper.GetViper())
}

func NewWithViper(v *viper.Viper) (ServerConfig, error) {
	var cfg ServerConfig
	if err := v.Unmarshal(&cfg, func(config *mapstructure.DecoderConfig) {
		config.DecodeHook = func(in reflect.Type, out reflect.Type, value interface{}) (interface{}, error) {
			if in.String() == "string" && out.String() == "slog.Level" {
				input := value.(string)
				var level slog.Level
				var err = level.UnmarshalText([]byte(input))
				if err == nil {
					return level, nil
				} else {
					return nil, err
				}
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

	slog.Info("Server Configuration: ")
	slog.Info(fmt.Sprintf("Webhook URL Base: %s", cfg.WebhookUrlBase))
	slog.Info(fmt.Sprintf("Webhook URL Prefix: %s", cfg.UrlPrefix))
	slog.Info(fmt.Sprintf("VCS Type: %s", cfg.VcsType))
	slog.Info(fmt.Sprintf("ArgoCD Namespace: %s", cfg.ArgoCDNamespace))

	return cfg, nil
}
