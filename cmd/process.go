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

		cfg, err := config.New()
		if err != nil {
			log.Fatal().Err(err).Msg("failed to generate config")
		}

		ctr, err := newContainer(ctx, cfg, false)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create container")
		}

		log.Info().Msg("initializing git settings")
		if err = initializeGit(ctr); err != nil {
			log.Fatal().Err(err).Msg("failed to initialize git settings")
		}

		repo, err := ctr.VcsClient.LoadHook(ctx, args[0])
		if err != nil {
			log.Fatal().Err(err).Msg("failed to load hook")
			return
		}

		processors, err := getProcessors(ctr)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create processors")
		}

		server.ProcessCheckEvent(ctx, repo, ctr, processors)
	},
}

func init() {
	RootCmd.AddCommand(processCmd)
}
