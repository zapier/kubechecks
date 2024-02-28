package cmd

import (
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/server"
)

var processCmd = &cobra.Command{
	Use:   "process",
	Short: "Process a pull request",
	Long:  "",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		cfg := config.ServerConfig{
			UrlPrefix:     "--unused--",
			WebhookSecret: "--unused--",
		}

		ctr, err := newContainer(ctx, cfg)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create container")
		}

		repo, err := ctr.VcsClient.LoadHook(ctx, args[0])
		if err != nil {
			log.Fatal().Err(err).Msg("failed to load hook")
			return
		}

		server.ProcessCheckEvent(ctx, repo, cfg, ctr)
	},
}

func init() {
	RootCmd.AddCommand(processCmd)
}
