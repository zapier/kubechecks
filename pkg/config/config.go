package config

import (
	"reflect"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/zapier/kubechecks/pkg"
)

// set default values in /cmd/root.go's init function

type ServerConfig struct {
	// argocd
	ArgoCDServerAddr         string `mapstructure:"argocd-api-server-addr"`
	ArgoCDToken              string `mapstructure:"argocd-api-token"`
	ArgoCDPathPrefix         string `mapstructure:"argocd-api-path-prefix"`
	ArgoCDInsecure           bool   `mapstructure:"argocd-api-insecure"`
	ArgoCDNamespace          string `mapstructure:"argocd-api-namespace"`
	ArgoCDPlainText          bool   `mapstructure:"argocd-api-plaintext"`
	ArgoCDRepositoryEndpoint string `mapstructure:"argocd-repository-endpoint"`
	ArgoCDRepositoryInsecure bool   `mapstructure:"argocd-repository-insecure"`
	ArgoCDSendFullRepository bool   `mapstructure:"argocd-send-full-repository"`
	ArgoCDIncludeDotGit      bool   `mapstructure:"argocd-include-dot-git"`
	KubernetesConfig         string `mapstructure:"kubernetes-config"`
	KubernetesType           string `mapstructure:"kubernetes-type"`
	KubernetesClusterID      string `mapstructure:"kubernetes-clusterid"`

	// otel
	EnableOtel        bool   `mapstructure:"otel-enabled"`
	OtelCollectorHost string `mapstructure:"otel-collector-host"`
	OtelCollectorPort string `mapstructure:"otel-collector-port"`

	// vcs
	VcsUsername  string `mapstructure:"vcs-username"`
	VcsEmail     string `mapstructure:"vcs-email"`
	VcsBaseUrl   string `mapstructure:"vcs-base-url"`
	VcsUploadUrl string `mapstructure:"vcs-upload-url"` // github enterprise upload URL
	VcsToken     string `mapstructure:"vcs-token"`
	VcsType      string `mapstructure:"vcs-type"`

	//github
	GithubPrivateKey     string `mapstructure:"github-private-key"`
	GithubAppID          int64  `mapstructure:"github-app-id"`
	GithubInstallationID int64  `mapstructure:"github-installation-id"`

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
	AdditionalAppsNamespaces []string      `mapstructure:"additional-apps-namespaces"`
	FallbackK8sVersion       string        `mapstructure:"fallback-k8s-version"`
	LabelFilter              string        `mapstructure:"label-filter"`
	LogLevel                 zerolog.Level `mapstructure:"log-level"`
	MonitorAllApplications   bool          `mapstructure:"monitor-all-applications"`
	OpenAIAPIToken           string        `mapstructure:"openai-api-token"`
	RepoRefreshInterval      time.Duration `mapstructure:"repo-refresh-interval"`
	RepoShallowClone         bool          `mapstructure:"repo-shallow-clone"`
	SchemasLocations         []string      `mapstructure:"schemas-location"`
	ShowDebugInfo            bool          `mapstructure:"show-debug-info"`
	TidyOutdatedCommentsMode string        `mapstructure:"tidy-outdated-comments-mode"`
	MaxQueueSize             int64         `mapstructure:"max-queue-size"`
	MaxConcurrenctChecks     int           `mapstructure:"max-concurrenct-checks"`
	ReplanCommentMessage     string        `mapstructure:"replan-comment-msg"`
	Identifier               string        `mapstructure:"identifier"`
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

			if in.String() == "string" && out.String() == "[]string" {
				input := value.(string)
				ns := strings.Split(input, ",")
				return ns, nil
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
	log.Info().Msgf("ArgoCD Namespace: %s", cfg.ArgoCDNamespace)

	return cfg, nil
}
