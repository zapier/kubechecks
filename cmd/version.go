package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zapier/kubechecks/pkg"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "List version information",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Arrgh\nVersion:%s\nSHA%s", pkg.GitTag, pkg.GitCommit)
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)
}
