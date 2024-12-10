package cmd

import (
	"os"
	"path/filepath"

	"github.com/argoproj/argo-cd/v2/common"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/zapier/kubechecks/pkg/container"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/server"
)

var processCmd = &cobra.Command{
	Use:   "process",
	Short: "Process a pull request",
	Long:  "",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		tempPath, err := os.MkdirTemp("", "")
		if err != nil {
			log.Fatal().Err(err).Msg("fail to create ssh data dir")
		}
		defer func() {
			os.RemoveAll(tempPath)
		}()

		// symlink local ssh known hosts to argocd ssh known hosts
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Fatal().Err(err).Msg("failed to get user home dir")
		}
		source := filepath.Join(homeDir, ".ssh", "known_hosts")
		target := filepath.Join(tempPath, common.DefaultSSHKnownHostsName)

		if err := os.Symlink(source, target); err != nil {
			log.Fatal().Err(err).Msg("fail to symlink ssh_known_hosts file")
		}

		if err := os.Setenv("ARGOCD_SSH_DATA_PATH", tempPath); err != nil {
			log.Fatal().Err(err).Msg("fail to set ARGOCD_SSH_DATA_PATH")
		}

		cfg, err := config.New()
		if err != nil {
			log.Fatal().Err(err).Msg("failed to generate config")
		}

		if len(args) != 1 {
			log.Fatal().Msg("usage: kubechecks process PR_REF")
		}

		ctr, err := container.New(ctx, cfg)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create clients")
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
