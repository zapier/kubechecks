package individual_check

import (
	"context"

	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/pkg/server"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
)

var vcsClient vcs_clients.Client // Currently, only allow one client at a time
var tokenUser string

func CheckIndividualBranch(prID int, repoName string) (string, error) {
	ctx := context.Background()
	vcsClient, _ := server.GetVCSClient()

	repo, err := vcsClient.GetPullRequestAsRepo(ctx, repoName, prID)
	if err != nil {
		panic("Failed to get Pull/Merge request!")
	}

	// Now we have the specific PR as a repo, we can process the traditional way
	labelFilter := viper.GetString("label-filter")

	// If we had a filter and we get here, we're good to process
	go server.ProcessCheckEvent(ctx, repo, labelFilter, vcsClient)
	return "Done", nil
}
