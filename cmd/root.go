package cmd

import (
	"context"
	"log/slog"
	"os"
	"strings"

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

const (
	LevelTrace = slog.Level(-8)
	LevelFatal = slog.Level(12)
)

var LevelNames = map[slog.Level]string{
	slog.LevelError: "ERROR",
	slog.LevelWarn:  "WARN",
	slog.LevelInfo:  "INFO",
	slog.LevelDebug: "DEBUG",
	LevelTrace:      "TRACE",
	LevelFatal:      "FATAL",
}

func init() {
	// allows environment variables to use _ instead of -
	viper.SetEnvKeyReplacer(envKeyReplacer) // sync-provider becomes SYNC_PROVIDER
	viper.SetEnvPrefix(envPrefix)           // port becomes KUBECHECKS_PORT
	viper.AutomaticEnv()                    // read in environment variables that match

	flags := RootCmd.PersistentFlags()
	stringFlag(flags, "log-level", "Set the log output level.",
		newStringOpts().
			withChoices(
				func() []string {
					values := make([]string, 0, len(LevelNames))
					for _, value := range LevelNames {
						values = append(values, value)
					}
					return values
				}()...,
			).
			withDefault("INFO").
			withShortHand("l"),
	)
	boolFlag(flags, "persist-log-level", "Persists the set log level down to other module loggers.")
	stringFlag(flags, "vcs-base-url", "VCS base url, useful if self hosting gitlab, enterprise github, etc.")
	stringFlag(flags, "vcs-type", "VCS type. One of gitlab or github. Defaults to gitlab.",
		newStringOpts().
			withChoices("github", "gitlab").
			withDefault("gitlab"))
	stringFlag(flags, "vcs-token", "VCS API token.")
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
	stringSliceFlag(flags, "schemas-location", "Sets schema locations to be used for every check request. Can be common paths inside the repos being checked or git urls in either git or http(s) format.")
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
	int64Flag(flags, "max-concurrenct-checks", "Number of concurrent checks to run.",
		newInt64Opts().
			withDefault(32))
	boolFlag(flags, "enable-hooks-renderer", "Render hooks.", newBoolOpts().withDefault(true))
	stringFlag(flags, "worst-hooks-state", "The worst state that can be returned from the hooks renderer.",
		newStringOpts().
			withDefault("panic"))

	panicIfError(viper.BindPFlags(flags))
	setupLogOutput()
}

func setupLogOutput() {
	ctx := context.Background()
	logLevel := &slog.LevelVar{} // INFO
	opts := &slog.HandlerOptions{
		Level: logLevel,
		// Map custom log levels to their string representation
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				levelLabel, exists := LevelNames[level]
				if !exists {
					levelLabel = level.String()
				}

				a.Value = slog.StringValue(levelLabel)
			}

			return a
		},
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, opts))

	// Retrieve log level from viper
	levelFlag := strings.ToUpper(viper.GetString("log-level"))
	level, err := ParseLevel(levelFlag)
	if err != nil {
		logLevel.Set(level)
	} else {
		logLevel.Set(slog.LevelInfo)
	}

	slog.SetDefault(logger)
	logger.Debug("Debug level logging enabled.")
	logger.Log(ctx, LevelTrace, "Trace level logging enabled.")
	logger.Info("Initialized logger.")

	// set logrus log level to overwrite the logs exporting from argo-cd package
	logrusLevel := logrus.ErrorLevel
	if viper.GetBool("persist_log_level") {
		if logger.Enabled(ctx, slog.LevelDebug) {
			logrusLevel = logrus.DebugLevel
		}
		if logger.Enabled(ctx, LevelTrace) {
			logrusLevel = logrus.TraceLevel
		}
	}

	logrus.StandardLogger().Level = logrusLevel
	logger.Info(
		"log_level", logrus.StandardLogger().Level.String(),
		"setting logrus log level",
	)
}

func ParseLevel(s string) (slog.Level, error) {
	var level slog.Level
	var err = level.UnmarshalText([]byte(s))
	return level, err
}

func LogFatal(ctx context.Context, msg string, keysAndValues ...interface{}) {
	slog.Log(ctx, LevelFatal, msg, keysAndValues...)
	os.Exit(1)
}
