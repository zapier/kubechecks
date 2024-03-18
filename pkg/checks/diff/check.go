package diff

import (
	"context"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/msg"
)

func Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	cr, rawDiff, err := getDiff(ctx, request.JsonManifests, request.App, request.Container, request.QueueApp, request.RemoveApp)
	if err != nil {
		return cr, err
	}

	aiDiffSummary(ctx, request.Note, request.Container.Config, request.AppName, request.JsonManifests, rawDiff)

	return cr, nil
}
