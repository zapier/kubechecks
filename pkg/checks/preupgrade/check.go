package preupgrade

import (
	"context"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/msg"
)

func Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	return checkApp(ctx, request.Container, request.AppName, request.KubernetesVersion, request.YamlManifests)
}
