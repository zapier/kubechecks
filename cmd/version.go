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
		fmt.Printf("kubechecks\nVersion:%s\nSHA%s\n", pkg.GitTag, pkg.GitCommit)
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)
}
