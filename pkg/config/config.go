package config

import (
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

type ServerConfig struct {
	// argocd
	ArgoCDServerAddr string
	ArgoCDToken      string
	ArgoCDPathPrefix string
	ArgoCDInsecure   bool

	// otel
	EnableOtel        bool
	OtelCollectorHost string
	OtelCollectorPort string

	// vcs
	VcsBaseUrl string
	VcsToken   string
	VcsType    string

	// webhooks
	EnsureWebhooks bool
	WebhookSecret  string
	WebhookUrlBase string

	// misc
	EnableConfTest           bool
	FallbackK8sVersion       string
	LabelFilter              string
	LogLevel                 zerolog.Level
	MonitorAllApplications   bool
	OpenAIAPIToken           string
	PoliciesLocation         []string
	SchemasLocations         []string
	ShowDebugInfo            bool
	TidyOutdatedCommentsMode string
	UrlPrefix                string
}

func New() (ServerConfig, error) {
	logLevelString := viper.GetString("log-level")
	logLevel, err := zerolog.ParseLevel(logLevelString)
	if err != nil {
		return ServerConfig{}, errors.Wrap(err, "failed to parse log level")
	}

	cfg := ServerConfig{
		ArgoCDInsecure:   viper.GetBool("argocd-api-insecure"),
		ArgoCDToken:      viper.GetString("argocd-api-token"),
		ArgoCDPathPrefix: viper.GetString("argocd-api-path-prefix"),
		ArgoCDServerAddr: viper.GetString("argocd-api-server-addr"),

		EnableConfTest:           viper.GetBool("enable-conftest"),
		EnableOtel:               viper.GetBool("otel-enabled"),
		EnsureWebhooks:           viper.GetBool("ensure-webhooks"),
		FallbackK8sVersion:       viper.GetString("fallback-k8s-version"),
		LabelFilter:              viper.GetString("label-filter"),
		LogLevel:                 logLevel,
		MonitorAllApplications:   viper.GetBool("monitor-all-applications"),
		OpenAIAPIToken:           viper.GetString("openai-api-token"),
		OtelCollectorHost:        viper.GetString("otel-collector-host"),
		OtelCollectorPort:        viper.GetString("otel-collector-port"),
		PoliciesLocation:         viper.GetStringSlice("policies-location"),
		SchemasLocations:         viper.GetStringSlice("schemas-location"),
		ShowDebugInfo:            viper.GetBool("show-debug-info"),
		TidyOutdatedCommentsMode: viper.GetString("tidy-outdated-comments-mode"),
		UrlPrefix:                viper.GetString("webhook-url-prefix"),
		WebhookSecret:            viper.GetString("webhook-secret"),
		WebhookUrlBase:           viper.GetString("webhook-url-base"),

		VcsBaseUrl: viper.GetString("vcs-base-url"),
		VcsToken:   viper.GetString("vcs-token"),
		VcsType:    viper.GetString("vcs-type"),
	}

	log.Info().Msg("Server Configuration: ")
	log.Info().Msgf("Webhook URL Base: %s", cfg.WebhookUrlBase)
	log.Info().Msgf("Webhook URL Prefix: %s", cfg.UrlPrefix)
	log.Info().Msgf("VCS Type: %s", cfg.VcsType)

	return cfg, nil
}
