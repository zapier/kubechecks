package cmd

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/server"
)

var processCmd = &cobra.Command{
	Use:   "process",
	Short: "Process a pull request",
	Long:  "",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.TODO()

		log.Info().Msg("building apps map from argocd")
		result, err := config.BuildAppsMap(ctx)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to build apps map")
		}

		clientType := viper.GetString("vcs-type")
		client, err := createVCSClient(clientType)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create vcs client")
		}

		cfg := config.ServerConfig{
			UrlPrefix:     "--unused--",
			WebhookSecret: "--unused--",
			VcsToArgoMap:  result,
			VcsClient:     client,
		}

		repo, err := client.LoadHook(ctx, args[0])
		if err != nil {
			log.Fatal().Err(err).Msg("failed to load hook")
			return
		}

		server.ProcessCheckEvent(ctx, repo, &cfg)
	},
}

func init() {
	RootCmd.AddCommand(processCmd)
}
