package hooks

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/sync/resource"
	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/msg"
)

const triple = "```"

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

		var renderedResources []string
		for _, r := range pw.resources {
			data, err := yaml.Marshal(r.Object)
			if err != nil {
				return msg.Result{}, errors.Wrap(err, "failed to unmarshal yaml")
			}

			renderedResources = append(renderedResources, "\n"+string(data))
		}

		var countFmt string
		resourceCount := len(renderedResources)
		switch resourceCount {
		case 0:
			continue
		case 1:
			countFmt = "%d resource"
		default:
			countFmt = "%d resources"
		}

		sectionName := fmt.Sprintf("%s phase, wave %d (%s)", pw.phase, pw.wave, countFmt)
		sectionName = fmt.Sprintf(sectionName, len(renderedResources))

		phaseDetail := fmt.Sprintf(`<details>
<summary>%s</summary>

`+triple+`yaml%s`+triple+`

</details>`, sectionName, strings.Join(renderedResources, "\n---\n"))

		phaseDetails = append(phaseDetails, phaseDetail)
	}

	return msg.Result{
		State:             pkg.StateNone,
		Summary:           fmt.Sprintf("<b>Sync Phases: %s</b>", strings.Join(toStringSlice(phaseNames), ", ")),
		Details:           strings.Join(phaseDetails, "\n\n"),
		NoChangesDetected: false,
	}, nil
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
