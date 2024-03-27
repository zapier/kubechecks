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
	"github.com/ghodss/yaml"
	"github.com/go-logr/zerologr"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

// from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
type objKeyLiveTarget struct {
	key    kube.ResourceKey
	live   *unstructured.Unstructured
	target *unstructured.Unstructured
}

func isAppMissingErr(err error) bool {
	return strings.Contains(err.Error(), "PermissionDenied")
}

//func Check(ctx context.Context, request checks.Request) (msg.Result, error) {
//	cr, rawDiff, err := getDiff(ctx, request.JsonManifests, request.App, request.Container, request.QueueApp, request.RemoveApp)
//	if err != nil {
//		return cr, err
//	}
//
//	return cr, nil
//}

/*
Check takes cli output and return as a string or an array of strings instead of printing

changedFilePath should be the root of the changed folder

from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
*/
func Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	ctx, span := tracer.Start(ctx, "getDiff")
	defer span.End()

	ctr := request.Container
	app := request.App
	manifests := request.JsonManifests
	removeApp := request.RemoveApp
	addApp := request.QueueApp

	argoClient := ctr.ArgoClient

	log.Debug().Str("name", app.Name).Msg("generating diff for application...")

	settingsCloser, settingsClient := argoClient.GetSettingsClient()
	defer settingsCloser.Close()

	closer, appClient := argoClient.GetApplicationClient()
	defer closer.Close()

	resources, err := appClient.ManagedResources(ctx, &application.ResourcesQuery{
		ApplicationName: &app.Name,
	})
	if err != nil {
		if !isAppMissingErr(err) {
			telemetry.SetError(span, err, "Get Argo Managed Resources")
			return msg.Result{}, err
		}

		resources = new(application.ManagedResourcesResponse)
	}

	items := make([]objKeyLiveTarget, 0)
	var unstructureds []*unstructured.Unstructured
	for _, mfst := range manifests {
		obj, err := argoappv1.UnmarshalToUnstructured(mfst)
		if err != nil {
			log.Warn().Err(err).Msg("failed to unmarshal to unstructured")
			continue
		}

		unstructureds = append(unstructureds, obj)
	}
	argoSettings, err := settingsClient.Get(ctx, &settings.SettingsQuery{})
	if err != nil {
		telemetry.SetError(span, err, "Get Argo Cluster Settings")
		return msg.Result{}, err
	}

	liveObjs, err := cmdutil.LiveObjects(resources.Items)
	if err != nil {
		telemetry.SetError(span, err, "Get Argo Live Objects")
		return msg.Result{}, err
	}

	groupedObjs, err := groupObjsByKey(unstructureds, liveObjs, app.Spec.Destination.Namespace)
	if err != nil {
		return msg.Result{}, err
	}

	if items, err = groupObjsForDiff(resources, groupedObjs, items, argoSettings, app.Name); err != nil {
		return msg.Result{}, err
	}

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
		if err != nil {
			telemetry.SetError(span, err, "Build Diff")
			return msg.Result{}, err
		}

		diffRes, err := argodiff.StateDiff(item.live, item.target, diffConfig)
		if err != nil {
			telemetry.SetError(span, err, "State Diff")
			return msg.Result{}, err
		}

		if diffRes.Modified || item.target == nil || item.live == nil {
			diffBuffer.WriteString(fmt.Sprintf("===== %s ======\n", resourceId))
			var live *unstructured.Unstructured
			var target *unstructured.Unstructured
			if item.target != nil && item.live != nil {
				target = &unstructured.Unstructured{}
				live = item.live
				if err = json.Unmarshal(diffRes.PredictedLive, target); err != nil {
					telemetry.SetError(span, err, "JSON Unmarshall")
					log.Warn().Err(err).Msg("failed to unmarshall json")
				}
			} else {
				live = item.live
				target = item.target
			}

			err := PrintDiff(diffBuffer, live, target)
			if err != nil {
				telemetry.SetError(span, err, "Print Diff")
				return msg.Result{}, err
			}
			switch {
			case item.target == nil:
				removed++
				if app, ok := isApp(item, diffRes.NormalizedLive); ok {
					removeApp(app)
				}
			case item.live == nil:
				added++
				if app, ok := isApp(item, diffRes.PredictedLive); ok {
					addApp(app)
				}
			case diffRes.Modified:
				modified++
				if app, ok := isApp(item, diffRes.PredictedLive); ok {
					addApp(app)
				}
			}
		}
	}

	var cr msg.Result

	if added != 0 || modified != 0 || removed != 0 {
		cr.Summary = fmt.Sprintf("%d added, %d modified, %d removed", added, modified, removed)
	} else {
		cr.Summary = "No changes"
		cr.NoChangesDetected = true
	}

	diff := diffBuffer.String()

	cr.Details = fmt.Sprintf("```diff\n%s\n```", diff)

	aiDiffSummary(ctx, request.Note, request.Container.Config, request.AppName, request.JsonManifests, diff)

	return cr, nil
}

var nilApp = argoappv1.Application{}

func isApp(item objKeyLiveTarget, manifests []byte) (argoappv1.Application, bool) {
	if strings.ToLower(item.key.Group) != "argoproj.io" {
		log.Debug().Str("group", item.key.Group).Msg("group is not correct")
		return nilApp, false
	}
	if strings.ToLower(item.key.Kind) != "application" {
		log.Debug().Str("kind", item.key.Kind).Msg("kind is not correct")
		return nilApp, false
	}

	var app argoappv1.Application
	if err := json.Unmarshal(manifests, &app); err != nil {
		log.Warn().Err(err).Msg("failed to deserialize application")
		return nilApp, false
	}

	return app, true
}

// from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
func groupObjsByKey(localObs []*unstructured.Unstructured, liveObjs []*unstructured.Unstructured, appNamespace string) (map[kube.ResourceKey]*unstructured.Unstructured, error) {
	namespacedByGk := make(map[schema.GroupKind]bool)
	for i := range liveObjs {
		if liveObjs[i] != nil {
			key := kube.GetResourceKey(liveObjs[i])
			namespacedByGk[schema.GroupKind{Group: key.Group, Kind: key.Kind}] = key.Namespace != ""
		}
	}
	localObs, _, err := controller.DeduplicateTargetObjects(appNamespace, localObs, &resourceInfoProvider{namespacedByGk: namespacedByGk})
	if err != nil {
		return nil, err
	}

	objByKey := make(map[kube.ResourceKey]*unstructured.Unstructured)
	for i := range localObs {
		obj := localObs[i]
		if !(hook.IsHook(obj) || ignore.Ignore(obj)) {
			objByKey[kube.GetResourceKey(obj)] = obj
		}
	}
	return objByKey, nil
}

// from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
func groupObjsForDiff(resources *application.ManagedResourcesResponse, objs map[kube.ResourceKey]*unstructured.Unstructured, items []objKeyLiveTarget, argoSettings *settings.Settings, appName string) ([]objKeyLiveTarget, error) {
	resourceTracking := argo.NewResourceTracking()
	for _, res := range resources.Items {
		var live = &unstructured.Unstructured{}
		if err := json.Unmarshal([]byte(res.NormalizedLiveState), &live); err != nil {
			return nil, err
		}

		key := kube.ResourceKey{Name: res.Name, Namespace: res.Namespace, Group: res.Group, Kind: res.Kind}
		if key.Kind == kube.SecretKind && key.Group == "" {
			// Don't bother comparing secrets, argo-cd doesn't have access to k8s secret data
			delete(objs, key)
			continue
		}
		if local, ok := objs[key]; ok || live != nil {
			if local != nil && !kube.IsCRD(local) {
				if err := resourceTracking.SetAppInstance(local, argoSettings.AppLabelKey, appName, "", argoappv1.TrackingMethod(argoSettings.GetTrackingMethod())); err != nil {
					return nil, err
				}
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

	return items, nil
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
