package kubeconform

import (
	"context"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/msg"
)

func Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	var repoDir string
	if request.Repo != nil {
		repoDir = request.Repo.Directory
	}

	return argoCdAppValidate(ctx, request.Container, request.AppName, request.KubernetesVersion, repoDir, request.YamlManifests)
}
