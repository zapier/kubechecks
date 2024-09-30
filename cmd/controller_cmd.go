package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
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

// ControllerCmd represents the run command
var ControllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Start the VCS Webhook handler.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		slog.Log(ctx, slog.LevelInfo, "Starting KubeChecks",
			"git-tag", pkg.GitTag,
			"git-commit", pkg.GitCommit,
		)

		slog.Info("parse configuration")
		cfg, err := config.New()
		if err != nil {
			LogFatal(ctx, "failed to parse configuration", "error", err)
		}

		ctr, err := newContainer(ctx, cfg, true)
		if err != nil {
			LogFatal(ctx, "failed to create container", "error", err)
		}

		slog.Info("initializing git settings")
		if err = initializeGit(ctr); err != nil {
			LogFatal(ctx, "failed to initialize git settings", "error", err)
		}

		if err = processLocations(ctx, ctr, cfg.PoliciesLocation); err != nil {
			LogFatal(ctx, "failed to process policy locations", "error", err)
		}
		if err = processLocations(ctx, ctr, cfg.SchemasLocations); err != nil {
			LogFatal(ctx, "failed to process schema locations", "error", err)
		}

		processors, err := getProcessors(ctr)
		if err != nil {
			LogFatal(ctx, "failed to create processors", "error", err)
		}

		t, err := initTelemetry(ctx, cfg)
		if err != nil {
			slog.Error("failed to initialize telemetry", "error", err)
			panic(err)
		}
		defer t.Shutdown()

		slog.Info("starting app watcher")
		startWebserver(ctx, ctr, processors)

		slog.Info("listening for requests")
		waitForShutdown()

		slog.Info("shutting down gracefully")
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
		slog.Info("waiting for in-flight requests to complete", "count", events.GetInFlight())
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
		slog.Debug("received signal", "signal", sig.String())
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
