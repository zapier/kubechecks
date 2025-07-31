package hooks

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/sync/resource"
	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/msg"
)

func Check(_ context.Context, request checks.Request) (msg.Result, error) {
	grouped := make(groupedSyncWaves)

	for _, manifest := range request.JsonManifests {
		obj, err := v1alpha1.UnmarshalToUnstructured(manifest)
		if err != nil {
			return msg.Result{}, errors.Wrap(err, "failed to parse manifest")
		}

		waves, err := phasesAndWaves(obj)
		if err != nil {
			return msg.Result{}, errors.Wrap(err, "failed to get phases and waves")
		}
		for _, hookInfo := range waves {
			grouped.addResource(hookInfo.hookType, hookInfo.hookWave, obj)
		}
	}

	var phaseNames []argocdSyncPhase
	var phaseDetails []string
	results := grouped.getSortedPhasesAndWaves()
	for _, pw := range results {
		if !slices.Contains(phaseNames, pw.phase) {
			phaseNames = append(phaseNames, pw.phase)
		}

		var waveDetails []string
		for _, w := range pw.waves {
			var resources []string
			for _, r := range w.resources {
				data, err := yaml.Marshal(r.Object)
				renderedResource := strings.TrimSpace(string(data))
				if err != nil {
					return msg.Result{}, errors.Wrap(err, "failed to unmarshal yaml")
				}

				renderedResource = collapsible(
					fmt.Sprintf("%s/%s %s/%s", r.GetAPIVersion(), r.GetKind(), r.GetNamespace(), r.GetName()),
					code("yaml", renderedResource),
				)
				resources = append(resources, renderedResource)
			}

			sectionName := fmt.Sprintf("Wave %d (%s)", w.wave, plural(resources, "resource", "resources"))
			waveDetail := collapsible(sectionName, strings.Join(resources, "\n\n"))
			waveDetails = append(waveDetails, waveDetail)
		}

		phaseDetail := collapsible(
			fmt.Sprintf("%s phase (%s)", pw.phase, plural(waveDetails, "wave", "waves")),
			strings.Join(waveDetails, "\n\n"),
		)
		phaseDetails = append(phaseDetails, phaseDetail)
	}

	if len(phaseNames) == 0 {
		return msg.Result{State: pkg.StateSkip}, nil
	}

	return msg.Result{
		State:             pkg.StateNone,
		Summary:           fmt.Sprintf("<b>Sync Phases: %s</b>", strings.Join(toStringSlice(phaseNames), ", ")),
		Details:           strings.Join(phaseDetails, "\n\n"),
		NoChangesDetected: false,
	}, nil
}

func plural[T any](items []T, singular, plural string) string {
	var description string
	if len(items) == 1 {
		description = singular
	} else {
		description = plural
	}

	return fmt.Sprintf("%d %s", len(items), description)
}

func toStringSlice(hookTypes []argocdSyncPhase) []string {
	result := make([]string, len(hookTypes))
	for idx := range hookTypes {
		result[idx] = string(hookTypes[idx])
	}
	return result
}

type hookInfo struct {
	hookType argocdSyncPhase
	hookWave waveNum
}

func code(format, content string) string {
	return fmt.Sprintf("```%s\n%s\n```", format, content)
}

func collapsible(summary, details string) string {
	return fmt.Sprintf("<details>\n<summary>%s</summary>\n\n%s\n</details>", summary, details)
}

func phasesAndWaves(obj *unstructured.Unstructured) ([]hookInfo, error) {
	var (
		syncWave  int64
		err       error
		hookInfos []hookInfo
	)

	syncWaveStr := obj.GetAnnotations()["argocd.argoproj.io/sync-wave"]
	if syncWaveStr == "" {
		syncWaveStr = obj.GetAnnotations()["helm.sh/hook-weight"]
	}
	if syncWaveStr != "" {
		if syncWave, err = strconv.ParseInt(syncWaveStr, 10, waveNumBits); err != nil {
			return nil, errors.Wrapf(err, "failed to parse sync wave %s", syncWaveStr)
		}
	}

	for hookType := range hookTypes(obj) {
		hookInfos = append(hookInfos, hookInfo{hookType: hookType, hookWave: waveNum(syncWave)})
	}

	return hookInfos, nil
}

// helm hook types: https://helm.sh/docs/topics/charts_hooks/
// helm to argocd map: https://argo-cd.readthedocs.io/en/stable/user-guide/helm/#helm-hooks
var helmHookToArgocdPhaseMap = map[string]argocdSyncPhase{
	"crd-install":  PreSyncPhase,
	"pre-install":  PreSyncPhase,
	"pre-upgrade":  PreSyncPhase,
	"post-upgrade": PostSyncPhase,
	"post-install": PostSyncPhase,
	"post-delete":  PostDeletePhase,
}

func hookTypes(obj *unstructured.Unstructured) map[argocdSyncPhase]struct{} {
	types := make(map[argocdSyncPhase]struct{})
	for _, text := range resource.GetAnnotationCSVs(obj, "argocd.argoproj.io/hook") {
		types[argocdSyncPhase(text)] = struct{}{}
	}

	// we ignore Helm hooks if we have Argo hook
	if len(types) == 0 {
		for _, text := range resource.GetAnnotationCSVs(obj, "helm.sh/hook") {
			if actualPhase := helmHookToArgocdPhaseMap[text]; actualPhase != "" {
				types[actualPhase] = struct{}{}
			}
		}
	}

	return types
}
