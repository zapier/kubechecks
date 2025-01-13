package diff

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	cmdutil "github.com/argoproj/argo-cd/v2/cmd/util"
	"github.com/argoproj/argo-cd/v2/controller"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/settings"
	argoappv1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/argo"
	argodiff "github.com/argoproj/argo-cd/v2/util/argo/diff"
	"github.com/argoproj/argo-cd/v2/util/argo/normalizers"
	"github.com/argoproj/gitops-engine/pkg/diff"
	"github.com/argoproj/gitops-engine/pkg/sync/hook"
	"github.com/argoproj/gitops-engine/pkg/sync/ignore"
	"github.com/argoproj/gitops-engine/pkg/utils/tracing"
	"github.com/ghodss/yaml"
	"github.com/go-logr/zerologr"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2/textlogger"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/gitops-engine/pkg/utils/kube"
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

/*
Check takes cli output and return as a string or an array of strings instead of printing

changedFilePath should be the root of the changed folder

from https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L879
*/
func Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	ctx, span := tracer.Start(ctx, "getDiff")
	defer span.End()

	app := request.App

	log.Debug().Str("name", app.Name).Msg("generating diff for application...")

	items := make([]objKeyLiveTarget, 0)
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
		return msg.Result{}, err
	}

	resources, err := getResources(ctx, request)
	if err != nil {
		return msg.Result{}, err
	}

	liveObjs, err := cmdutil.LiveObjects(resources)
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

	var diffBuffer strings.Builder
	var added, modified, removed int
	for _, item := range items {
		resourceId := fmt.Sprintf("%s/%s %s/%s", item.key.Group, item.key.Kind, item.key.Namespace, item.key.Name)
		log.Trace().Str("resource", resourceId).Msg("diffing object")

		if item.target != nil && hook.IsHook(item.target) || item.live != nil && hook.IsHook(item.live) {
			continue
		}

		diffRes, err := generateDiff(ctx, request, argoSettings, item)
		if err != nil {
			return msg.Result{}, err
		}

		if diffRes.Modified || item.target == nil || item.live == nil {
			err := addResourceDiffToMessage(ctx, &diffBuffer, resourceId, item, diffRes)
			if err != nil {
				return msg.Result{}, err
			}

			processResources(item, diffRes, request, &added, &modified, &removed)
		}
	}

	var cr msg.Result

	if added != 0 || modified != 0 || removed != 0 {
		cr.Summary = fmt.Sprintf("%d added, %d modified, %d removed", added, modified, removed)
	} else {
		cr.Summary = "No changes"
		cr.NoChangesDetected = true
	}

	renderedDiff := diffBuffer.String()

	cr.Details = fmt.Sprintf("```diff\n%s\n```", renderedDiff)

	aiDiffSummary(ctx, request.Note, request.Container.Config, request.AppName, renderedDiff)

	return cr, nil
}

func processResources(item objKeyLiveTarget, diffRes diff.DiffResult, request checks.Request, added, modified, removed *int) {
	switch {
	case item.target == nil:
		*removed++
		if app, ok := isApp(item, diffRes.NormalizedLive); ok {
			request.RemoveApp(app)
		}
	case item.live == nil:
		*added++
		if app, ok := isApp(item, diffRes.PredictedLive); ok {
			request.QueueApp(app)
		}
	case diffRes.Modified:
		*modified++
		if app, ok := isApp(item, diffRes.PredictedLive); ok {
			request.QueueApp(app)
		}
	}
}

func addResourceDiffToMessage(ctx context.Context, diffBuffer *strings.Builder, resourceId string, item objKeyLiveTarget, diffRes diff.DiffResult) error {
	_, span := tracer.Start(ctx, "addResourceDiffToMessage")
	defer span.End()

	diffBuffer.WriteString(fmt.Sprintf("===== %s ======\n", resourceId))

	var live *unstructured.Unstructured
	var target *unstructured.Unstructured
	if item.target != nil && item.live != nil {
		target = &unstructured.Unstructured{}
		live = item.live
		if err := json.Unmarshal(diffRes.PredictedLive, target); err != nil {
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
		return err
	}

	return nil
}

func generateDiff(ctx context.Context, request checks.Request, argoSettings *settings.Settings, item objKeyLiveTarget) (diff.DiffResult, error) {
	_, span := tracer.Start(ctx, "getResources")
	defer span.End()

	overrides := make(map[string]argoappv1.ResourceOverride)
	for k := range argoSettings.ResourceOverrides {
		val := argoSettings.ResourceOverrides[k]
		overrides[k] = *val
	}

	ignoreAggregatedRoles := false
	ignoreNormalizerOpts := normalizers.IgnoreNormalizerOpts{
		JQExecutionTimeout: 1 * time.Second,
	}
	kubeCtl := &kube.KubectlCmd{
		Tracer: tracing.NopTracer{},
		Log:    textlogger.NewLogger(textlogger.NewConfig()),
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return diff.DiffResult{}, err
	}
	apiRes, _, err := kubeCtl.LoadOpenAPISchema(config)
	if err != nil {
		return diff.DiffResult{}, err
	}
	resources, _, err := kubeCtl.ManageResources(config, apiRes)
	if err != nil {
		return diff.DiffResult{}, err
	}
	dryRunner := diff.NewK8sServerSideDryRunner(resources)

	diffConfig, err := argodiff.NewDiffConfigBuilder().
		WithLogger(zerologr.New(&log.Logger)).
		WithDiffSettings(request.App.Spec.IgnoreDifferences, overrides, ignoreAggregatedRoles, ignoreNormalizerOpts).
		WithTracking(argoSettings.AppLabelKey, argoSettings.TrackingMethod).
		WithNoCache().
		WithIgnoreMutationWebhook(false).
		WithServerSideDiff(true).
		WithServerSideDryRunner(dryRunner).
		WithManager("application/apply-patch").
		Build()
	if err != nil {
		telemetry.SetError(span, err, "Build Diff")
		return diff.DiffResult{}, err
	}

	diffRes, err := argodiff.StateDiff(item.live, item.target, diffConfig)
	if err != nil {
		telemetry.SetError(span, err, "State Diff")
		return diff.DiffResult{}, err
	}
	return diffRes, nil
}

func getResources(ctx context.Context, request checks.Request) ([]*argoappv1.ResourceDiff, error) {
	ctx, span := tracer.Start(ctx, "getResources")
	defer span.End()

	closer, appClient := request.Container.ArgoClient.GetApplicationClient()
	defer closer.Close()

	resources, err := appClient.ManagedResources(ctx, &application.ResourcesQuery{
		ApplicationName: &request.App.Name,
	})
	if err != nil {
		if isAppMissingErr(err) {
			span.RecordError(err)
			return nil, nil
		}

		return nil, err
	}
	return resources.Items, nil
}

func getArgoSettings(ctx context.Context, request checks.Request) (*settings.Settings, error) {
	ctx, span := tracer.Start(ctx, "getArgoSettings")
	defer span.End()

	settingsCloser, settingsClient := request.Container.ArgoClient.GetSettingsClient()
	defer settingsCloser.Close()

	argoSettings, err := settingsClient.Get(ctx, &settings.SettingsQuery{})
	if err != nil {
		telemetry.SetError(span, err, "Get Argo Cluster Settings")
		return nil, err
	}
	return argoSettings, nil
}

var nilApp = argoappv1.Application{}

func isApp(item objKeyLiveTarget, manifests []byte) (argoappv1.Application, bool) {
	logger := log.With().
		Str("kind", item.key.Kind).
		Str("name", item.key.Name).
		Str("namespace", item.key.Namespace).
		Str("group", item.key.Group).
		Logger()

	if strings.ToLower(item.key.Group) != "argoproj.io" {
		logger.Debug().Msg("group is not correct")
		return nilApp, false
	}
	if strings.ToLower(item.key.Kind) != "application" {
		logger.Debug().Msg("kind is not correct")
		return nilApp, false
	}

	var app argoappv1.Application
	if err := json.Unmarshal(manifests, &app); err != nil {
		logger.Warn().Err(err).Msg("failed to deserialize application")
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
func groupObjsForDiff(resources []*argoappv1.ResourceDiff, objs map[kube.ResourceKey]*unstructured.Unstructured, items []objKeyLiveTarget, argoSettings *settings.Settings, appName string) ([]objKeyLiveTarget, error) {
	resourceTracking := argo.NewResourceTracking()
	for _, res := range resources {
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
				if err := resourceTracking.SetAppInstance(
					local, argoSettings.AppLabelKey, appName, "", argoappv1.TrackingMethod(argoSettings.GetTrackingMethod()), "",
				); err != nil {
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

// IsNamespaced infers if obj is namespaced or not from corresponding live objects list. If corresponding live object
// has namespace then target object is also namespaced. If live object is missing then it does not matter if target is
// namespaced or not.
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
