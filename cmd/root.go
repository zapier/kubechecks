package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:              "kubechecks",
	Short:            "Argo Git Hooks",
	Long:             `A Kubernetes controller and webhook server for integration of ArgoCD applications into CI`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.CheckErr(RootCmd.Execute())
}

const envPrefix = "kubechecks"

var envKeyReplacer = strings.NewReplacer("-", "_")

func init() {
	// allows environment variables to use _ instead of -
	viper.SetEnvKeyReplacer(envKeyReplacer) // sync-provider becomes SYNC_PROVIDER
	viper.SetEnvPrefix(envPrefix)           // port becomes KUBECHECKS_PORT
	viper.AutomaticEnv()                    // read in environment variables that match

	flags := RootCmd.PersistentFlags()
	stringFlag(flags, "log-level", "Set the log output level.",
		newStringOpts().
			withChoices(
				zerolog.LevelErrorValue,
				zerolog.LevelWarnValue,
				zerolog.LevelInfoValue,
				zerolog.LevelDebugValue,
				zerolog.LevelTraceValue,
			).
			withDefault(zerolog.LevelInfoValue).
			withShortHand("l"),
	)
	boolFlag(flags, "persist-log-level", "Persists the set log level down to other module loggers.")
	stringFlag(flags, "vcs-base-url", "VCS base url, useful if self hosting gitlab, enterprise github, etc.")
	stringFlag(flags, "vcs-upload-url", "VCS upload url, required for enterprise github.")
	stringFlag(flags, "vcs-type", "VCS type. One of gitlab, github, or gitea.",
		newStringOpts().
			withChoices("github", "gitlab", "gitea").
			withDefault("gitlab"))
	stringFlag(flags, "vcs-token", "VCS API token.")
	stringFlag(flags, "vcs-username", "VCS Username.")
	stringFlag(flags, "vcs-email", "VCS Email.")
	stringFlag(flags, "github-private-key", "Github App Private Key.")
	int64Flag(flags, "github-app-id", "Github App ID.")
	int64Flag(flags, "github-installation-id", "Github Installation ID.")
	stringFlag(flags, "argocd-api-token", "ArgoCD API token.")
	stringFlag(flags, "argocd-api-server-addr", "ArgoCD API Server Address.",
		newStringOpts().
			withDefault("argocd-server"))
	boolFlag(flags, "argocd-api-insecure", "Enable to use insecure connections over TLS to the ArgoCD API server.")
	stringFlag(flags, "argocd-api-namespace", "ArgoCD namespace where the application watcher will read Custom Resource Definitions (CRD) for Application and ApplicationSet resources.",
		newStringOpts().
			withDefault("argocd"))
	boolFlag(flags, "argocd-api-plaintext", "Enable to use plaintext connections without TLS.")
	stringFlag(flags, "kubernetes-type", "Kubernetes Type One of eks, or local. Defaults to local.",
		newStringOpts().
			withChoices("eks", "local").
			withDefault("local"))
	stringFlag(flags, "kubernetes-clusterid", "Kubernetes Cluster ID, must be specified if kubernetes-type is eks.")
	stringFlag(flags, "kubernetes-config", "Path to your kubernetes config file, used to monitor applications.")

	stringFlag(flags, "otel-collector-port", "The OpenTelemetry collector port.")
	stringFlag(flags, "otel-collector-host", "The OpenTelemetry collector host.")
	boolFlag(flags, "otel-enabled", "Enable OpenTelemetry.")

	stringFlag(flags, "tidy-outdated-comments-mode", "Sets the mode to use when tidying outdated comments.",
		newStringOpts().
			withChoices("hide", "delete").
			withDefault("hide"))
	stringSliceFlag(flags, "schemas-location", "Sets schema locations to be used for every check request. Can be a common path on the host or git urls in either git or http(s) format.")
	boolFlag(flags, "enable-conftest", "Set to true to enable conftest policy checking of manifests.")
	stringSliceFlag(flags, "policies-location", "Sets rego policy locations to be used for every check request. Can be common path inside the repos being checked or git urls in either git or http(s) format.",
		newStringSliceOpts().
			withDefault([]string{"./policies"}))
	stringFlag(flags, "worst-conftest-state", "The worst state that can be returned from conftest.",
		newStringOpts().
			withDefault("panic"))
	boolFlag(flags, "enable-kubeconform", "Enable kubeconform checks.",
		newBoolOpts().
			withDefault(true))
	stringFlag(flags, "worst-kubeconform-state", "The worst state that can be returned from kubeconform.",
		newStringOpts().
			withDefault("panic"))
	boolFlag(flags, "enable-preupgrade", "Enable preupgrade checks.",
		newBoolOpts().
			withDefault(true))
	stringFlag(flags, "worst-preupgrade-state", "The worst state that can be returned from preupgrade checks.",
		newStringOpts().
			withDefault("panic"))
	int64Flag(flags, "max-queue-size", "Size of app diff check queue.",
		newInt64Opts().
			withDefault(1024))
	int64Flag(flags, "max-concurrent-checks", "Number of concurrent checks to run.",
		newInt64Opts().
			withDefault(32))
	int64Flag(flags, "max-repo-worker-queue-size", "Maximum size of check request queue per repository worker.",
		newInt64Opts().
			withDefault(100))
	boolFlag(flags, "enable-hooks-renderer", "Render hooks.", newBoolOpts().withDefault(true))
	stringFlag(flags, "worst-hooks-state", "The worst state that can be returned from the hooks renderer.",
		newStringOpts().
			withDefault("panic"))
	stringFlag(flags, "replan-comment-msg", "comment message which re-triggers kubechecks on PR.",
		newStringOpts().
			withDefault("kubechecks again"))
	stringSliceFlag(flags, "additional-apps-namespaces", "Additional namespaces other than the ArgoCDNamespace to monitor for applications.")
	boolFlag(flags, "repo-cache-enabled", "Enable persistent repository caching.",
		newBoolOpts().
			withDefault(true))
	stringFlag(flags, "repo-cache-dir", "Directory for persistent repository cache.",
		newStringOpts().
			withDefault("/tmp/kubechecks/repos"))
	durationFlag(flags, "repo-cache-ttl", "Time-to-live for cached repositories.",
		newDurationOpts().
			withDefault(24*time.Hour))
	stringFlag(flags, "archive-cache-dir", "Directory for archive cache.",
		newStringOpts().
			withDefault("/tmp/kubechecks/archives"))
	durationFlag(flags, "archive-cache-ttl", "Time-to-live for cached archives.",
		newDurationOpts().
			withDefault(1*time.Hour))
	stringFlag(flags, "identifier", "Identifier for the kubechecks instance. Used to differentiate between multiple kubechecks instances.",
		newStringOpts().
			withDefault(""))
	stringFlag(flags, "kubepug-generated-store", "URL for the kubepug generated store.",
		newStringOpts().
			withDefault("https://kubepug.xyz/data/data.json"))

	panicIfError(viper.BindPFlags(flags))
	setupLogOutput()
}

func setupLogOutput() {
	// Default level is info, unless debug flag is present
	levelFlag := viper.GetString("log-level")
	level, err := zerolog.ParseLevel(levelFlag)
	if err != nil {
		log.Error().Err(err).Msg("Invalid log level")
	}

	zerolog.SetGlobalLevel(level)

	// short the caller output to file:line only
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		return filepath.Base(file) + ":" + strconv.Itoa(line)
	}
	output := zerolog.ConsoleWriter{Out: os.Stdout}
	log.Logger = log.Output(output)

	log.Debug().Caller().Msg("Debug level logging enabled.")
	log.Trace().Msg("Trace level logging enabled.")
	log.Info().Msg("Initialized logger.")

	// set logrus log level to overwrite the logs exporting from argo-cd package
	logrusLevel := logrus.ErrorLevel
	if viper.GetBool("persist_log_level") {
		if log.Debug().Enabled() {
			logrusLevel = logrus.DebugLevel
		}
		if log.Trace().Enabled() {
			logrusLevel = logrus.TraceLevel
		}
	}

	logrus.StandardLogger().Level = logrusLevel
	log.Info().Str("log_level", logrus.StandardLogger().Level.String()).Msg("setting logrus log level")

}
