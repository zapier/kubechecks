package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/pkg/app_watcher"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/events"
	"github.com/zapier/kubechecks/pkg/server"
	"github.com/zapier/kubechecks/telemetry"
)

// ControllerCmd represents the run command
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

		ctr, err := container.New(ctx, cfg)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create container")
		}

		// watch app modifications, if necessary
		if cfg.MonitorAllApplications {
			appWatcher, err := app_watcher.NewApplicationWatcher(ctr, ctx)
			if err != nil {
				log.Fatal().Err(err).Msg("failed to create watch applications")
			}
			go appWatcher.Run(ctx, 1)

			appSetWatcher, err := app_watcher.NewApplicationSetWatcher(ctr, ctx)
			if err != nil {
				log.Fatal().Err(err).Msg("failed to create watch application sets")
			}
			go appSetWatcher.Run(ctx)
		} else {
			log.Info().Msgf("not monitoring applications, MonitorAllApplications: %+v", cfg.MonitorAllApplications)
		}

		log.Info().Strs("locations", cfg.PoliciesLocation).Msg("processing policies locations")
		if err = processLocations(ctx, ctr, cfg.PoliciesLocation); err != nil {
			log.Fatal().Err(err).Msg("failed to process policy locations")
		}

		log.Info().Strs("locations", cfg.SchemasLocations).Msg("processing schemas locations")
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
		defer t.Shutdown()

		// Create server
		srv := server.NewServer(ctr, processors)

		// Start HTTP server in background
		log.Info().Msg("starting web server")
		go func() {
			if err := srv.Start(ctx); err != nil {
				log.Fatal().Err(err).Msg("failed to start web server")
			}
		}()

		// Wait for shutdown signal
		log.Info().Msg("listening for requests")
		waitForShutdown()

		// Begin graceful shutdown with 30 second timeout
		log.Info().Msg("shutting down gracefully")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Wait for in-flight requests to complete (with timeout)
		waitForPendingRequest(shutdownCtx)

		// Shutdown HTTP server and queue workers
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("server shutdown failed")
		}

		// Shutdown kubechecks controller resources such as repo worker
		ctr.Shutdown()

		log.Info().Msg("shutdown complete")
	},
}

func initTelemetry(ctx context.Context, cfg config.ServerConfig) (*telemetry.OperatorTelemetry, error) {
	return telemetry.Init(
		ctx, "kubechecks", pkg.GitTag, pkg.GitCommit,
		cfg.EnableOtel, cfg.OtelCollectorHost, cfg.OtelCollectorPort,
	)
}

// waitForPendingRequest waits for all in-flight requests to complete or until the context is done.
func waitForPendingRequest(ctx context.Context) {
	inFlight := events.GetInFlight()
	if inFlight == 0 {
		log.Info().Msg("no in-flight requests to wait for")
		return
	}

	log.Info().Int("count", inFlight).Msg("waiting for in-flight requests to complete")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			remaining := events.GetInFlight()
			if remaining > 0 {
				log.Warn().Int("count", remaining).Msg("shutdown timeout reached with in-flight requests remaining")
			}
			return
		case <-ticker.C:
			inFlight := events.GetInFlight()
			if inFlight == 0 {
				log.Info().Msg("all in-flight requests completed")
				return
			}
			log.Info().Int("count", inFlight).Msg("still waiting for in-flight requests")
		}
	}
}

// waitForShutdown blocks until a termination signal is received.
func waitForShutdown() {
	// graceful termination handler.
	// when we receive a SIGTERM/SIGINT, initiate graceful shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	sig := <-sigs
	log.Info().Str("signal", sig.String()).Msg("received shutdown signal")
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

	stringFlag(flags, "argocd-repository-endpoint", `Location of the argocd repository service endpoint.`,
		newStringOpts().withDefault("argocd-repo-server.argocd:8081"))
	boolFlag(flags, "argocd-repository-insecure", `True if you need to skip validating the grpc tls certificate.`,
		newBoolOpts().withDefault(true))
	boolFlag(flags, "argocd-send-full-repository", `Set to true if you want to try to send the full repository to ArgoCD when generating manifests.`)
	stringFlag(flags, "label-filter", `(Optional) If set, The label that must be set on an MR (as "kubechecks:<value>") for kubechecks to process the merge request webhook (KUBECHECKS_LABEL_FILTER).`)
	stringFlag(flags, "openai-api-token", "OpenAI API Token.")
	stringFlag(flags, "webhook-url-base", "The endpoint to listen on for incoming PR/MR event webhooks. For example, 'https://checker.mycompany.com'.")
	stringFlag(flags, "webhook-url-prefix", "If your application is running behind a proxy that uses path based routing, set this value to match the path prefix. For example, '/hello/world'.")
	stringFlag(flags, "webhook-secret", "Optional secret key for validating the source of incoming webhooks.")
	boolFlag(flags, "monitor-all-applications", "Monitor all applications in argocd automatically.",
		newBoolOpts().withDefault(true))
	boolFlag(flags, "ensure-webhooks", "Ensure that webhooks are created in repositories referenced by argo.")
	stringFlag(flags, "repo-refresh-interval", "Interval between static repo refreshes (for schemas and policies).",
		newStringOpts().withDefault("5m"))

	panicIfError(viper.BindPFlags(flags))
}
