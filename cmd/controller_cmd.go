package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/events"
	"github.com/zapier/kubechecks/pkg/server"
)

// ControllerCmd represents the run command
var ControllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Start the VCS Webhook handler.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting KubeChecks:", pkg.GitTag, pkg.GitCommit)

		server := server.NewServer(&config.ServerConfig{
			UrlPrefix:     viper.GetString("webhook-url-prefix"),
			WebhookSecret: viper.GetString("webhook-secret"),
		})
		go server.Start()

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
		log.Info().Msg("shutting down...")
		for events.GetInFlight() > 0 {
			log.Info().Int("count", events.GetInFlight()).Msg("waiting for in-flight requests to complete")
			time.Sleep(time.Second * 3)
		}
		log.Info().Msg("good bye.")

	},
	PreRunE: func(cmd *cobra.Command, args []string) error {
		log.Info().Msg("Server Configuration: ")
		log.Info().Msgf("Webhook URL Base: %s", viper.GetString("webhook-url-base"))
		log.Info().Msgf("Webhook URL Prefix: %s", viper.GetString("webhook-url-prefix"))
		log.Info().Msgf("VCS Type: %s", viper.GetString("vcs-type"))
		return nil
	},
}

func panicIfError(err error) {
	if err != nil {
		panic(err)
	}
}

func init() {
	RootCmd.AddCommand(ControllerCmd)

	flags := ControllerCmd.Flags()
	stringFlag(flags, "fallback-k8s-version", "Fallback target Kubernetes version for schema / upgrade checks.",
		newStringOpts().
			withDefault("1.23.0"))
	boolFlag(flags, "show-debug-info", "Set to true to print debug info to the footer of MR comments.")
	boolFlag(flags, "enable-conftest", "Set to true to enable conftest policy checking of manifests.")
	stringFlag(flags, "label-filter", `(Optional) If set, The label that must be set on an MR (as "kubechecks:<value>") for kubechecks to process the merge request webhook.`)
	stringFlag(flags, "openai-api-token", "OpenAI API Token.")
	stringFlag(flags, "webhook-url-base", "The URL where KubeChecks receives webhooks from Gitlab.")
	stringFlag(flags, "webhook-url-prefix", "If your application is running behind a proxy that uses path based routing, set this value to match the path prefix.")
	stringFlag(flags, "webhook-secret", "Optional secret key for validating the source of incoming webhooks.")
	boolFlag(flags, "monitor-all-applications", "Monitor all applications in argocd automatically.")
	boolFlag(flags, "ensure-webhooks", "Ensure that webhooks are created in repositories referenced by argo.")

	panicIfError(viper.BindPFlags(flags))
}
