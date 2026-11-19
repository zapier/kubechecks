package diff

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/argoproj/argo-cd/gitops-engine/pkg/utils/kube"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/settings"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func configMap(data string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "app",
		},
		"data": map[string]any{"key": data},
	}}
}

func marshalState(t *testing.T, obj *unstructured.Unstructured) string {
	t.Helper()
	state, err := json.Marshal(obj.Object)
	require.NoError(t, err)
	return string(state)
}

func TestLiveStateForDiff(t *testing.T) {
	live := configMap("old")
	liveState := marshalState(t, live)

	tests := map[string]struct {
		normalized string
		raw        string
		wantNil    bool
	}{
		"uses the normalized live state":                  {normalized: liveState, raw: "null"},
		"falls back to the raw state on comparison error": {normalized: "{}", raw: liveState},
		"falls back to the raw state when empty":          {normalized: "", raw: liveState},
		"resource missing from the cluster":               {normalized: "null", raw: "null", wantNil: true},
		"no state at all":                                 {wantNil: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := liveStateForDiff(&argoappv1.ResourceDiff{NormalizedLiveState: tc.normalized, LiveState: tc.raw})
			require.NoError(t, err)
			if tc.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, "ConfigMap", got.GetKind())
			assert.Equal(t, "settings", got.GetName())
		})
	}
}

// When Argo CD reports a ComparisonError it returns "{}" for every resource's
// normalized live state; the diff must still run against the raw live object
// instead of failing with "Object 'Kind' is missing in '{}'".
func TestDiffWithComparisonErrorLiveState(t *testing.T) {
	live := configMap("old")
	// The raw live state carries cluster-populated fields the normalized state omits.
	live.SetAnnotations(map[string]string{trackingIDAnnotation: "app:/ConfigMap:app/settings"})
	live.SetResourceVersion("12345")
	live.Object["metadata"].(map[string]any)["managedFields"] = []any{map[string]any{"manager": "argocd-controller", "operation": "Apply"}}
	live.Object["status"] = map[string]any{"observedGeneration": int64(3)}
	live.Object["data"].(map[string]any)["ignored"] = "set-by-a-controller"

	key := kube.ResourceKey{Name: "settings", Namespace: "app", Kind: "ConfigMap"}
	resources := []*argoappv1.ResourceDiff{{
		Name:                key.Name,
		Namespace:           key.Namespace,
		Kind:                key.Kind,
		NormalizedLiveState: "{}",
		LiveState:           marshalState(t, live),
	}}
	argoSettings := &settings.Settings{}
	request := newRequest("app", "argocd", "argocd")
	request.App.Spec.IgnoreDifferences = argoappv1.IgnoreDifferences{{
		Kind:         "ConfigMap",
		JSONPointers: []string{"/data/ignored"},
	}}

	for name, tc := range map[string]struct {
		target       *unstructured.Unstructured
		wantModified bool
		wantChanges  []string
	}{
		"unchanged resource": {target: configMap("old"), wantModified: false},
		"changed resource":   {target: configMap("new"), wantModified: true, wantChanges: []string{"-  key: old", "+  key: new"}},
	} {
		t.Run(name, func(t *testing.T) {
			objs := map[kube.ResourceKey]*unstructured.Unstructured{key: tc.target}
			items, err := groupObjsForDiff(resources, objs, nil, argoSettings, "app", "app")
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.NotNil(t, items[0].live)

			diffRes, err := generateDiff(context.Background(), request, argoSettings, items[0])
			require.NoError(t, err)
			assert.Equal(t, tc.wantModified, diffRes.Modified)
			if !diffRes.Modified {
				return
			}

			// The posted diff must show only real changes, not cluster-populated or
			// ignoreDifferences fields that exist only in the raw live state.
			var rendered strings.Builder
			require.NoError(t, addResourceDiffToMessage(context.Background(), &rendered, "/ConfigMap app/settings", items[0], diffRes))
			var changes []string
			for _, line := range strings.Split(rendered.String(), "\n") {
				if (strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")) &&
					!strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "+++") {
					changes = append(changes, line)
				}
			}
			assert.Equal(t, tc.wantChanges, changes, rendered.String())
		})
	}
}
