package diff

import (
	"context"
	"fmt"
	"strings"

	cmdutil "github.com/argoproj/argo-cd/v3/cmd/util"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/sync/hook"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/telemetry"
)

// GenerateDiffText computes a unified diff string for the given request.
// This extracts the diff generation logic so it can be reused by other checks (e.g., AI review).
func GenerateDiffText(ctx context.Context, request checks.Request) (string, error) {
	ctx, span := tracer.Start(ctx, "GenerateDiffText")
	defer span.End()

	app := request.App

	var unstructureds []*unstructured.Unstructured
	for _, mfst := range request.JsonManifests {
		obj, err := argoappv1.UnmarshalToUnstructured(mfst)
		if err != nil {
			log.Warn().Err(err).Msg("failed to unmarshal to unstructured")
			continue
		}
		unstructureds = append(unstructureds, obj)
	}

	argoSettings, err := getArgoSettings(ctx, request)
	if err != nil {
		return "", err
	}

	resources, err := getResources(ctx, request)
	if err != nil {
		return "", err
	}

	liveObjs, err := cmdutil.LiveObjects(resources)
	if err != nil {
		telemetry.SetError(span, err, "Get Argo Live Objects")
		return "", err
	}

	groupedObjs, err := groupObjsByKey(unstructureds, liveObjs, app.Spec.Destination.Namespace)
	if err != nil {
		return "", err
	}

	items := make([]objKeyLiveTarget, 0)
	if items, err = groupObjsForDiff(resources, groupedObjs, items, argoSettings, app.Name, app.Spec.Destination.Namespace); err != nil {
		return "", err
	}

	var diffBuffer strings.Builder
	for _, item := range items {
		if item.target != nil && hook.IsHook(item.target) || item.live != nil && hook.IsHook(item.live) {
			continue
		}

		diffRes, err := generateDiff(ctx, request, argoSettings, item)
		if err != nil {
			return "", err
		}

		if diffRes.Modified || item.target == nil || item.live == nil {
			resourceId := fmt.Sprintf("%s/%s %s/%s", item.key.Group, item.key.Kind, item.key.Namespace, item.key.Name)
			err := addResourceDiffToMessage(ctx, &diffBuffer, resourceId, item, diffRes)
			if err != nil {
				return "", err
			}
		}
	}

	return diffBuffer.String(), nil
}
