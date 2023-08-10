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

const EnvPrefix = "kubechecks"

var EnvKeyReplacer = strings.NewReplacer("-", "_")

func init() {

	// allows environment variables to use _ instead of -
	viper.SetEnvKeyReplacer(EnvKeyReplacer) // sync-provider becomes SYNC_PROVIDER
	viper.SetEnvPrefix(EnvPrefix)           // port becomes KUBECHECKS_PORT
	viper.AutomaticEnv()                    // read in environment variables that match

	flags := RootCmd.PersistentFlags()
	flags.StringP("log-level", "l", "info", "Set the log output level (info, debug, trace)")
	flags.Bool("persist_log_level", false, "Persists the set log level down to other module loggers")
	flags.String("vcs-base-url", "", "VCS base url, useful if self hosting gitlab, enterprise github, etc")
	flags.String("vcs-type", "gitlab", "VCS type, one of gitlab or github. Defaults to gitlab (KUBECHECKS_VCS_TYPE).")
	flags.String("vcs-token", "", "VCS API token (KUBECHECKS_VCS_TOKEN).")
	flags.String("argocd-api-token", "", "ArgoCD API token (KUBECHECKS_ARGOCD_API_TOKEN).")
	flags.String("argocd-api-server-addr", "argocd-server", "ArgoCD API Server Address (KUBECHECKS_ARGOCD_API_SERVER_ADDR).")
	flags.Bool("argocd-api-insecure", false, "Enable to use insecure connections to the ArgoCD API server (KUBECHECKS_ARGOCD_API_INSECURE).")

	flags.String("otel-collector-port", "", "The OpenTelemetry collector port (KUBECHECKS_OTEL_COLLECTOR_PORT).")
	flags.String("otel-collector-host", "", "The OpenTelemetry collector host (KUBECHECKS_OTEL_COLLECTOR_HOST).")
	flags.Bool("otel-enabled", false, "Enable OpenTelemetry (KUBECHECKS_OTEL_ENABLED).")

	flags.StringP("tidy-outdated-comments-mode", "", "hide", "Sets the mode to use when tidying outdated comments. Defaults to hide. Other options are delete, hide (KUBECHECKS_TIDY_OUTDATED_COMMENTS_MODE).")
	flags.StringP("schemas-location", "", "./schemas", "Sets the schema location. Can be local path or git repository. Defaults to ./schemas (KUBECHECKS_SCHEMAS_LOCATION).")

	viper.BindPFlags(flags)

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
