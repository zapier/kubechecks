package diff

import (
	"encoding/json"
	"testing"

	"github.com/argoproj/argo-cd/gitops-engine/pkg/utils/kube"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/settings"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
)

const trackingIDAnnotation = "argocd.argoproj.io/tracking-id"

func newRequest(appName, appNamespace, argocdNamespace string) checks.Request {
	return checks.Request{
		App: argoappv1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: appNamespace},
		},
		Container: container.Container{
			Config: config.ServerConfig{ArgoCDNamespace: argocdNamespace},
		},
	}
}

func TestAppInstanceName(t *testing.T) {
	t.Run("app in the controller namespace keeps its bare name", func(t *testing.T) {
		request := newRequest("plasm-integrations", "cluster-management", "cluster-management")
		argoSettings := &settings.Settings{ControllerNamespace: "cluster-management"}

		assert.Equal(t, "plasm-integrations", appInstanceName(request, argoSettings))
	})

	t.Run("app in another namespace is qualified with it", func(t *testing.T) {
		request := newRequest("plasm-integrations", "integrations", "cluster-management")
		argoSettings := &settings.Settings{ControllerNamespace: "cluster-management"}

		assert.Equal(t, "integrations_plasm-integrations", appInstanceName(request, argoSettings))
	})

	t.Run("falls back to the configured namespace when settings omit it", func(t *testing.T) {
		argoSettings := &settings.Settings{}

		// Without the fallback an app in the Argo CD namespace would be qualified
		// against "", picking up a prefix Argo CD never writes.
		local := newRequest("plasm-integrations", "cluster-management", "cluster-management")
		assert.Equal(t, "plasm-integrations", appInstanceName(local, argoSettings))

		remote := newRequest("plasm-integrations", "integrations", "cluster-management")
		assert.Equal(t, "integrations_plasm-integrations", appInstanceName(remote, argoSettings))
	})
}

// A resource rendered for an app that lives outside the Argo CD namespace must be
// stamped with the same qualified tracking id Argo CD wrote to the cluster,
// otherwise every resource of every such app reports an annotation-only diff.
func TestGroupObjsForDiffSetsQualifiedTrackingID(t *testing.T) {
	const (
		liveTrackingID = "integrations_plasm-integrations:policy/PodDisruptionBudget:integrations/plasm-billing"
		appNamespace   = "integrations"
	)

	target := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "policy/v1",
		"kind":       "PodDisruptionBudget",
		"metadata": map[string]any{
			"name":      "plasm-billing",
			"namespace": appNamespace,
		},
	}}

	live := target.DeepCopy()
	live.SetAnnotations(map[string]string{trackingIDAnnotation: liveTrackingID})
	liveState, err := json.Marshal(live.Object)
	require.NoError(t, err)

	key := kube.ResourceKey{Name: "plasm-billing", Namespace: appNamespace, Group: "policy", Kind: "PodDisruptionBudget"}
	resources := []*argoappv1.ResourceDiff{{
		Name:                key.Name,
		Namespace:           key.Namespace,
		Group:               key.Group,
		Kind:                key.Kind,
		NormalizedLiveState: string(liveState),
	}}
	objs := map[kube.ResourceKey]*unstructured.Unstructured{key: target}

	argoSettings := &settings.Settings{
		AppLabelKey:         "argocd.argoproj.io/instance",
		TrackingMethod:      string(argoappv1.TrackingMethodAnnotation),
		ControllerNamespace: "cluster-management",
	}

	request := newRequest("plasm-integrations", appNamespace, "cluster-management")

	items, err := groupObjsForDiff(resources, objs, nil, argoSettings, appInstanceName(request, argoSettings), appNamespace)
	require.NoError(t, err)
	require.Len(t, items, 1)

	require.NotNil(t, items[0].target)
	assert.Equal(t, liveTrackingID, items[0].target.GetAnnotations()[trackingIDAnnotation])
}
