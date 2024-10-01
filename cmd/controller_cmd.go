package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/events"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/server"
	"github.com/zapier/kubechecks/telemetry"
)

// ControllerCmd represents the run command.
var ControllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Start the VCS Webhook handler.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		log.Info().
			Str("git-tag", pkg.GitTag).
			Str("git-commit", pkg.GitCommit).
			Msg("Starting KubeChecks")

		log.Info().Msg("parsing configuration")
		cfg, err := config.New()
		if err != nil {
			log.Fatal().Err(err).Msg("failed to parse configuration")
		}

		ctr, err := newContainer(ctx, cfg, true)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create container")
		}

		log.Info().Msg("initializing git settings")
		if err = initializeGit(ctr); err != nil {
			log.Fatal().Err(err).Msg("failed to initialize git settings")
		}

		if err = processLocations(ctx, ctr, cfg.PoliciesLocation); err != nil {
			log.Fatal().Err(err).Msg("failed to process policy locations")
		}
		if err = processLocations(ctx, ctr, cfg.SchemasLocations); err != nil {
			log.Fatal().Err(err).Msg("failed to process schema locations")
		}

		processors, err := getProcessors(ctr)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create processors")
		}

		t, err := initTelemetry(ctx, cfg)
		if err != nil {
			log.Panic().Err(err).Msg("Failed to initialize telemetry")
		}
		defer t.Shutdown(ctx)

		log.Info().Msgf("starting web server")
		startWebserver(ctx, ctr, processors)

		log.Info().Msgf("listening for requests")
		waitForShutdown()

		log.Info().Msg("shutting down gracefully")
		waitForPendingRequest()
	},
}

func initTelemetry(ctx context.Context, cfg config.ServerConfig) (*telemetry.OperatorTelemetry, error) {
	return telemetry.Init(
		ctx, "kubechecks", pkg.GitTag, pkg.GitCommit,
		cfg.EnableOtel, cfg.OtelCollectorHost, cfg.OtelCollectorPort,
	)
}

func startWebserver(ctx context.Context, ctr container.Container, processors []checks.ProcessorEntry) {
	srv := server.NewServer(ctr, processors)
	go srv.Start(ctx)
}

func initializeGit(ctr container.Container) error {
	if err := git.SetCredentials(ctr.Config, ctr.VcsClient); err != nil {
		return err
	}

	return nil
}

func waitForPendingRequest() {
	for events.GetInFlight() > 0 {
		log.Info().Int("count", events.GetInFlight()).Msg("waiting for in-flight requests to complete")
		time.Sleep(time.Second * 3)
	}
}

func waitForShutdown() {
	// graceful termination handler.
	// when we receive a SIGTERM from kubernetes, check for in-flight requests before exiting.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	done := make(chan bool, 1)

	go func() {
		sig := <-sigs
		log.Debug().Str("signal", sig.String()).Msg("received signal")
		done <- true
	}()

	<-done
}

func panicIfError(err error) {
	if err != nil {
		panic(err)
	}
}

func init() {
	RootCmd.AddCommand(ControllerCmd)

	flags := ControllerCmd.Flags()
	stringFlag(flags, "fallback-k8s-version", "Fallback target Kubernetes version for schema / upgrade checks (KUBECHECKS_FALLBACK_K8S_VERSION).",
		newStringOpts().withDefault("1.23.0"))
	boolFlag(flags, "show-debug-info", "Set to true to print debug info to the footer of MR comments (KUBECHECKS_SHOW_DEBUG_INFO).")

	stringFlag(flags, "label-filter", `(Optional) If set, The label that must be set on an MR (as "kubechecks:<value>") for kubechecks to process the merge request webhook (KUBECHECKS_LABEL_FILTER).`)
	stringFlag(flags, "openai-api-token", "OpenAI API Token.")
	stringFlag(flags, "webhook-url-base", "The endpoint to listen on for incoming PR/MR event webhooks. For example, 'https://checker.mycompany.com'.")
	stringFlag(flags, "webhook-url-prefix", "If your application is running behind a proxy that uses path based routing, set this value to match the path prefix. For example, '/hello/world'.")
	stringFlag(flags, "webhook-secret", "Optional secret key for validating the source of incoming webhooks.")
	boolFlag(flags, "monitor-all-applications", "Monitor all applications in argocd automatically.")
	boolFlag(flags, "ensure-webhooks", "Ensure that webhooks are created in repositories referenced by argo.")
	stringFlag(flags, "repo-refresh-interval", "Interval between static repo refreshes (for schemas and policies).",
		newStringOpts().withDefault("5m"))

	panicIfError(viper.BindPFlags(flags))
}
