package cmd

import (
	"log/slog"

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
			LogFatal(ctx, "failed to generate config", "error", err)
		}

		ctr, err := newContainer(ctx, cfg, false)
		if err != nil {
			LogFatal(ctx, "failed to create container", "error", err)
		}

		slog.Info("initializing git settings")
		if err = initializeGit(ctr); err != nil {
			LogFatal(ctx, "failed to initialize git settings", "error", err)
		}

		repo, err := ctr.VcsClient.LoadHook(ctx, args[0])
		if err != nil {
			LogFatal(ctx, "failed to load hook", "error", err)
			return
		}

		processors, err := getProcessors(ctr)
		if err != nil {
			LogFatal(ctx, "failed to create processors", "error", err)
		}

		server.ProcessCheckEvent(ctx, repo, ctr, processors)
	},
}

func init() {
	RootCmd.AddCommand(processCmd)
}
