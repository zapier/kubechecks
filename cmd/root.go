package cmd

import (
	"context"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/telemetry"
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
	ctx := context.Background()
	t, err := initTelemetry(ctx)
	if err != nil {
		log.Panic().Err(err).Msg("Failed to initialize telemetry")
	}

	defer t.Shutdown()

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
			withChoices("info", "debug", "trace").
			withDefault("info").
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
	stringFlag(flags, "argocd-api-server-addr", "ArgoCD API Server Address.", newStringOpts().withDefault("argocd-server"))
	boolFlag(flags, "argocd-api-insecure", "Enable to use insecure connections to the ArgoCD API server.")

	stringFlag(flags, "otel-collector-port", "The OpenTelemetry collector port.")
	stringFlag(flags, "otel-collector-host", "The OpenTelemetry collector host.")
	boolFlag(flags, "otel-enabled", "Enable OpenTelemetry.")

	stringFlag(flags, "tidy-outdated-comments-mode", "Sets the mode to use when tidying outdated comments.",
		newStringOpts().
			withChoices("hide", "delete").
			withDefault("hide"),
	)
	stringFlag(flags, "schemas-location", "Sets the schema location. Can be local path or git repository.",
		newStringOpts().
			withDefault("./schemas"))

	panicIfError(viper.BindPFlags(flags))

	setupLogOutput()

}

func initTelemetry(ctx context.Context) (*telemetry.OperatorTelemetry, error) {
	enableOtel := viper.GetBool("otel-enabled")
	otelHost := viper.GetString("otel-collector-host")
	otelPort := viper.GetString("otel-collector-port")
	return telemetry.Init(ctx, "kubechecks", enableOtel, otelHost, otelPort)
}

func setupLogOutput() {
	output := zerolog.ConsoleWriter{Out: os.Stdout}
	log.Logger = log.Output(output)

	// Default level is info, unless debug flag is present
	levelFlag := viper.GetString("log-level")
	level, _ := zerolog.ParseLevel(levelFlag)

	zerolog.SetGlobalLevel(level)
	log.Debug().Msg("Debug level logging enabled.")
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
