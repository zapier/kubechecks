package cmd

import (
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/zapier/kubechecks/pkg"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "List version information",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		log.Info().Msgf("kubechecks\nVersion:%s\nSHA%s\n", pkg.GitTag, pkg.GitCommit)
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)
}
