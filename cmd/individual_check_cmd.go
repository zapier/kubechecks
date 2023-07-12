package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/individual_check"
)

// Github Actions/local run
var individualCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check a specific PR/MR",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting KubeChecks:", pkg.GitTag, pkg.GitCommit)
		individual_check.CheckIndividualBranch(viper.GetInt("pr-id"), viper.GetString("repo"))
	},
}

func init() {
	rootCmd.AddCommand(individualCheckCmd)

	flags := individualCheckCmd.Flags()
	flags.String("fallback-k8s-version", "1.23.0", "Fallback target Kubernetes version for schema / upgrade checks (KUBECHECKS_FALLBACK_K8S_VERSION).")
	flags.Bool("show-debug-info", false, "Set to true to print debug info to the footer of MR comments (KUBECHECKS_SHOW_DEBUG_INFO).")
	flags.Bool("enable-conftest", false, "Set to true to enable conftest policy checking of manifests (KUBECHECKS_ENABLE_CONFTEST).")
	flags.String("label-filter", "", "(Optional) If set, The label that must be set on an MR (as \"kubechecks:<value>\") for kubechecks to process the merge request webhook (KUBECHECKS_LABEL_FILTER).")
	flags.String("openai-api-token", "", "OpenAI API Token (KUBECHECKS_OPENAI_API_TOKEN).")
	flags.String("vcs-type", "gitlab", "The type of VCS provider (gitlab|github).")
	flags.Int("pr-id", 0, "The ID of the PR/MR to check (KUBECHECKS_PR_ID).")
	flags.String("repo", "", "The name of the repo to check (KUBECHECKS_REPO).")
	// Map viper to cobra flags so we can get these parameters from Environment variables if set.
	viper.BindPFlag("enable-conftest", flags.Lookup("enable-conftest"))
	viper.BindPFlag("fallback-k8s-version", flags.Lookup("fallback-k8s-version"))
	viper.BindPFlag("show-debug-info", flags.Lookup("show-debug-info"))
	viper.BindPFlag("label-filter", flags.Lookup("label-filter"))
	viper.BindPFlag("openai-api-token", flags.Lookup("openai-api-token"))
	viper.BindPFlag("vcs-type", flags.Lookup("vcs-type"))
	viper.BindPFlag("pr-id", flags.Lookup("pr-id"))
	viper.BindPFlag("repo", flags.Lookup("repo"))
}
