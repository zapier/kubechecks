package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zapier/kubechecks/pkg/events"

	_ "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/server"
)

// controllerCmd represents the run command
var controllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Start the VCS Webhook handler.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting KubeChecks:", pkg.GitTag, pkg.GitCommit)

		server := server.NewServer(&server.ServerConfig{
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
		return nil
	},
}

func init() {
	rootCmd.AddCommand(controllerCmd)

	flags := controllerCmd.Flags()
	flags.String("fallback-k8s-version", "1.23.0", "Fallback target Kubernetes version for schema / upgrade checks (KUBECHECKS_FALLBACK_K8S_VERSION).")
	flags.Bool("show-debug-info", false, "Set to true to print debug info to the footer of MR comments (KUBECHECKS_SHOW_DEBUG_INFO).")
	flags.Bool("enable-conftest", false, "Set to true to enable conftest policy checking of manifests (KUBECHECKS_SHOW_DEBUG_INFO).")
	flags.String("label-filter", "", "(Optional) If set, The label that must be set on an MR (as \"kubechecks:<value>\") for kubechecks to process the merge request webhook (KUBECHECKS_LABEL_FILTER).")
	flags.String("openai-api-token", "", "OpenAI API Token (KUBECHECKS_OPENAI_API_TOKEN).")
	flags.String("webhook-url-base", "", "The URL where KubeChecks receives webhooks from Gitlab")
	flags.String("webhook-url-prefix", "", "If your application is running behind a proxy that uses path based routing, set this value to match the path prefix.")
	flags.String("webhook-secret", "", "Optional secret key for validating the source of incoming webhooks.")
	flags.String("vcs-type", "gitlab", "The type of VCS provider (gitlab|github).")
	// Map viper to cobra flags so we can get these parameters from Environment variables if set.
	viper.BindPFlag("enable-conftest", flags.Lookup("enable-conftest"))
	viper.BindPFlag("fallback-k8s-version", flags.Lookup("fallback-k8s-version"))
	viper.BindPFlag("show-debug-info", flags.Lookup("show-debug-info"))
	viper.BindPFlag("label-filter", flags.Lookup("label-filter"))
	viper.BindPFlag("openai-api-token", flags.Lookup("openai-api-token"))
	viper.BindPFlag("webhook-url-base", flags.Lookup("webhook-url-base"))
	viper.BindPFlag("webhook-url-prefix", flags.Lookup("webhook-url-prefix"))
	viper.BindPFlag("webhook-secret", flags.Lookup("webhook-secret"))
	viper.BindPFlag("vcs-type", flags.Lookup("vcs-type"))
}
