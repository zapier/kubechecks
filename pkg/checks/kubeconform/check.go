package kubeconform

import (
	"context"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/msg"
)

func Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	return argoCdAppValidate(
		ctx, request.Container, request.AppName, request.KubernetesVersion, request.Repo.Directory,
		request.YamlManifests,
	)
}
