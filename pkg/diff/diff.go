package diff

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	cmdutil "github.com/argoproj/argo-cd/v2/cmd/util"
	"github.com/argoproj/argo-cd/v2/controller"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/settings"
	argoappv1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/argo"
	argodiff "github.com/argoproj/argo-cd/v2/util/argo/diff"
	"github.com/argoproj/gitops-engine/pkg/sync/hook"
	"github.com/argoproj/gitops-engine/pkg/sync/ignore"
	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	"github.com/argoproj/pkg/errors"
	"github.com/ghodss/yaml"
	"github.com/go-logr/zerologr"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/telemetry"
)

// from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
type objKeyLiveTarget struct {
	key    kube.ResourceKey
	live   *unstructured.Unstructured
	target *unstructured.Unstructured
}

/*
Take cli output and return as a string or an array of strings instead of printing

changedFilePath should be the root of the changed folder

from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
*/
func GetDiff(ctx context.Context, name string, manifests []string, app *argoappv1.Application, addApp func(*argoappv1.Application)) (pkg.CheckResult, string, error) {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "GetDiff")
	defer span.End()

	argoClient := argo_client.GetArgoClient()
	closer, appClient := argoClient.GetApplicationClient()

	log.Debug().Str("name", name).Msg("generating diff for application...")

	defer closer.Close()

	settingsCloser, settingsClient := argoClient.GetSettingsClient()
	defer settingsCloser.Close()

	var (
		err       error
		resources *application.ManagedResourcesResponse
	)

	appName := name
	if app == nil {
		app, err = appClient.Get(ctx, &application.ApplicationQuery{
			Name: &appName,
		})
		if err != nil {
			telemetry.SetError(span, err, "Get Argo App")
			return pkg.CheckResult{}, "", err
		}

		resources, err = appClient.ManagedResources(ctx, &application.ResourcesQuery{
			ApplicationName: &appName,
		})
		if err != nil {
			telemetry.SetError(span, err, "Get Argo Managed Resources")
			return pkg.CheckResult{}, "", err
		}
	} else {
		resources = new(application.ManagedResourcesResponse)
	}

	errors.CheckError(err)
	items := make([]objKeyLiveTarget, 0)
	var unstructureds []*unstructured.Unstructured
	for _, mfst := range manifests {
		obj, err := argoappv1.UnmarshalToUnstructured(mfst)
		errors.CheckError(err)
		unstructureds = append(unstructureds, obj)
	}
	argoSettings, err := settingsClient.Get(ctx, &settings.SettingsQuery{})
	if err != nil {
		telemetry.SetError(span, err, "Get Argo Cluster Settings")
		return pkg.CheckResult{}, "", err
	}

	liveObjs, err := cmdutil.LiveObjects(resources.Items)
	if err != nil {
		telemetry.SetError(span, err, "Get Argo Live Objects")
		return pkg.CheckResult{}, "", err
	}

	groupedObjs := groupObjsByKey(unstructureds, liveObjs, app.Spec.Destination.Namespace)
	items = groupObjsForDiff(resources, groupedObjs, items, argoSettings, app.Name)
	diffBuffer := &strings.Builder{}
	var added, modified, removed int
	for _, item := range items {
		resourceId := fmt.Sprintf("%s/%s %s/%s", item.key.Group, item.key.Kind, item.key.Namespace, item.key.Name)
		log.Trace().Str("resource", resourceId).Msg("diffing object")
		if item.target != nil && hook.IsHook(item.target) || item.live != nil && hook.IsHook(item.live) {
			continue
		}
		overrides := make(map[string]argoappv1.ResourceOverride)
		for k := range argoSettings.ResourceOverrides {
			val := argoSettings.ResourceOverrides[k]
			overrides[k] = *val
		}

		// TODO remove hardcoded IgnoreAggregatedRoles and retrieve the
		// compareOptions in the protobuf
		ignoreAggregatedRoles := false
		diffConfig, err := argodiff.NewDiffConfigBuilder().
			WithLogger(zerologr.New(&log.Logger)).
			WithDiffSettings(app.Spec.IgnoreDifferences, overrides, ignoreAggregatedRoles).
			WithTracking(argoSettings.AppLabelKey, argoSettings.TrackingMethod).
			WithNoCache().
			Build()
		errors.CheckError(err)
		diffRes, err := argodiff.StateDiff(item.live, item.target, diffConfig)
		errors.CheckError(err)

		if diffRes.Modified || item.target == nil || item.live == nil {
			diffBuffer.WriteString(fmt.Sprintf("===== %s ======\n", resourceId))
			var live *unstructured.Unstructured
			var target *unstructured.Unstructured
			if item.target != nil && item.live != nil {
				target = &unstructured.Unstructured{}
				live = item.live
				err = json.Unmarshal(diffRes.PredictedLive, target)
				errors.CheckError(err)
			} else {
				live = item.live
				target = item.target
			}

			err := PrintDiff(diffBuffer, live, target)
			if err != nil {
				telemetry.SetError(span, err, "Print Diff")
				return pkg.CheckResult{}, "", err
			}
			switch {
			case item.target == nil:
				removed++
			case item.live == nil:
				added++
				if app, ok := isApp(item, diffRes.PredictedLive); ok {
					addApp(app)
				}
			case diffRes.Modified:
				modified++
			}
		}
	}
	var summary string
	if added != 0 || modified != 0 || removed != 0 {
		summary = fmt.Sprintf("%d added, %d modified, %d removed", added, modified, removed)
	} else {
		summary = "No changes"
	}

	diff := diffBuffer.String()

	var cr pkg.CheckResult
	cr.Summary = summary
	cr.Details = fmt.Sprintf("```diff\n%s\n```", diff)

	return cr, diff, nil
}

func isApp(item objKeyLiveTarget, manifests []byte) (*argoappv1.Application, bool) {
	if strings.ToLower(item.key.Group) != "argoproj.io" {
		log.Debug().Str("group", item.key.Group).Msg("group is not correct")
		return nil, false
	}
	if strings.ToLower(item.key.Kind) != "application" {
		log.Debug().Str("kind", item.key.Kind).Msg("kind is not correct")
		return nil, false
	}

	var app argoappv1.Application
	if err := json.Unmarshal(manifests, &app); err != nil {
		log.Warn().Err(err).Msg("failed to deserialize application")
		return nil, false
	}

	return &app, true
}

// from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
func groupObjsByKey(localObs []*unstructured.Unstructured, liveObjs []*unstructured.Unstructured, appNamespace string) map[kube.ResourceKey]*unstructured.Unstructured {
	namespacedByGk := make(map[schema.GroupKind]bool)
	for i := range liveObjs {
		if liveObjs[i] != nil {
			key := kube.GetResourceKey(liveObjs[i])
			namespacedByGk[schema.GroupKind{Group: key.Group, Kind: key.Kind}] = key.Namespace != ""
		}
	}
	localObs, _, err := controller.DeduplicateTargetObjects(appNamespace, localObs, &resourceInfoProvider{namespacedByGk: namespacedByGk})
	errors.CheckError(err)
	objByKey := make(map[kube.ResourceKey]*unstructured.Unstructured)
	for i := range localObs {
		obj := localObs[i]
		if !(hook.IsHook(obj) || ignore.Ignore(obj)) {
			objByKey[kube.GetResourceKey(obj)] = obj
		}
	}
	return objByKey
}

// from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
func groupObjsForDiff(resources *application.ManagedResourcesResponse, objs map[kube.ResourceKey]*unstructured.Unstructured, items []objKeyLiveTarget, argoSettings *settings.Settings, appName string) []objKeyLiveTarget {
	resourceTracking := argo.NewResourceTracking()
	for _, res := range resources.Items {
		var live = &unstructured.Unstructured{}
		err := json.Unmarshal([]byte(res.NormalizedLiveState), &live)
		errors.CheckError(err)

		key := kube.ResourceKey{Name: res.Name, Namespace: res.Namespace, Group: res.Group, Kind: res.Kind}
		if key.Kind == kube.SecretKind && key.Group == "" {
			// Don't bother comparing secrets, argo-cd doesn't have access to k8s secret data
			delete(objs, key)
			continue
		}
		if local, ok := objs[key]; ok || live != nil {
			if local != nil && !kube.IsCRD(local) {
				err = resourceTracking.SetAppInstance(local, argoSettings.AppLabelKey, appName, "", argoappv1.TrackingMethod(argoSettings.GetTrackingMethod()))
				errors.CheckError(err)
			}

			items = append(items, objKeyLiveTarget{key, live, local})
			delete(objs, key)
		}
	}
	for key, local := range objs {
		if key.Kind == kube.SecretKind && key.Group == "" {
			// Don't bother comparing secrets, argo-cd doesn't have access to k8s secret data
			delete(objs, key)
			continue
		}
		items = append(items, objKeyLiveTarget{key, nil, local})
	}
	return items
}

// from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
type resourceInfoProvider struct {
	namespacedByGk map[schema.GroupKind]bool
}

// Infer if obj is namespaced or not from corresponding live objects list. If corresponding live object has namespace then target object is also namespaced.
// If live object is missing then it does not matter if target is namespaced or not.
func (p *resourceInfoProvider) IsNamespaced(gk schema.GroupKind) (bool, error) {
	return p.namespacedByGk[gk], nil
}

// PrintDiff prints a diff between two unstructured objects to stdout using an external diff utility
// Honors the diff utility set in the KUBECTL_EXTERNAL_DIFF environment variable
func PrintDiff(w io.Writer, live *unstructured.Unstructured, target *unstructured.Unstructured) error {
	var err error
	targetData := []byte("")
	if target != nil {
		targetData, err = yaml.Marshal(target)
		if err != nil {
			return err
		}
	}

	liveData := []byte("")
	if live != nil {
		liveData, err = yaml.Marshal(live)
		if err != nil {
			return err
		}
	}

	diff := difflib.UnifiedDiff{
		A: difflib.SplitLines(string(liveData)),
		B: difflib.SplitLines(string(targetData)),
		// FromFile: "Original",
		// ToFile:   "Current",
		Context: 2,
	}

	return difflib.WriteUnifiedDiff(w, diff)
	//return difflib.GetUnifiedDiffString(diff)
	// return dmp.DiffPrettyText(diff), nil
}
