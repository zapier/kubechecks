package hooks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
)

type data map[string]any

func toJson(obj data) string {
	data, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}

	return string(data)
}

func TestCheck(t *testing.T) {
	ctx := context.Background()

	preSyncHookAndDefaultSyncWave := data{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": data{
			"name":      "preSyncHookAndDefaultSyncWave",
			"namespace": "some-namespace",
			"annotations": data{
				"argocd.argoproj.io/hook": "PreSync",
			},
		},
	}

	preSyncHookAndNonDefaultSyncWave := data{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": data{
			"name":      "preSyncHookAndNonDefaultSyncWave",
			"namespace": "some-namespace",
			"annotations": data{
				"argocd.argoproj.io/hook":      "PreSync",
				"argocd.argoproj.io/sync-wave": "5",
			},
		},
	}

	postSyncHookAndNonDefaultSyncWave := data{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": data{
			"name":      "postSyncHookAndNonDefaultSyncWave",
			"namespace": "some-namespace",
			"annotations": data{
				"argocd.argoproj.io/hook":      "PostSync",
				"argocd.argoproj.io/sync-wave": "5",
			},
		},
	}

	helmPostInstallHook := data{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": data{
			"name":      "helmPostInstallHook",
			"namespace": "other-namespace",
			"annotations": data{
				"helm.sh/hook": "post-install",
			},
		},
	}

	helmPostInstallHookWithWeight := data{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": data{
			"name":      "helmPostInstallHookWithWeight",
			"namespace": "other-namespace",
			"annotations": data{
				"helm.sh/hook":        "post-install",
				"helm.sh/hook-weight": "5",
			},
		},
	}

	req := checks.Request{
		JsonManifests: []string{
			toJson(preSyncHookAndDefaultSyncWave),
			toJson(preSyncHookAndNonDefaultSyncWave),
			toJson(postSyncHookAndNonDefaultSyncWave),
			toJson(helmPostInstallHook),
			toJson(helmPostInstallHookWithWeight),
		},
	}

	triple := "```"

	res, err := Check(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, pkg.StateNone, res.State)
	assert.Equal(t, "<b>Sync Phases: PreSync, PostSync</b>", res.Summary)

	expected := `<details>
<summary>PreSync phase (2 waves)</summary>

<details>
<summary>Wave 0 (1 resource)</summary>

<details>
<summary>v1/ConfigMap some-namespace/preSyncHookAndDefaultSyncWave</summary>

` + triple + `yaml
apiVersion: v1
kind: ConfigMap
metadata:
  annotations:
    argocd.argoproj.io/hook: PreSync
  name: preSyncHookAndDefaultSyncWave
  namespace: some-namespace
` + triple + `
</details>
</details>

<details>
<summary>Wave 5 (1 resource)</summary>

<details>
<summary>v1/ConfigMap some-namespace/preSyncHookAndNonDefaultSyncWave</summary>

` + triple + `yaml
apiVersion: v1
kind: ConfigMap
metadata:
  annotations:
    argocd.argoproj.io/hook: PreSync
    argocd.argoproj.io/sync-wave: "5"
  name: preSyncHookAndNonDefaultSyncWave
  namespace: some-namespace
` + triple + `
</details>
</details>
</details>

<details>
<summary>PostSync phase (2 waves)</summary>

<details>
<summary>Wave 0 (1 resource)</summary>

<details>
<summary>v1/ConfigMap other-namespace/helmPostInstallHook</summary>

` + triple + `yaml
apiVersion: v1
kind: ConfigMap
metadata:
  annotations:
    helm.sh/hook: post-install
  name: helmPostInstallHook
  namespace: other-namespace
` + triple + `
</details>
</details>

<details>
<summary>Wave 5 (2 resources)</summary>

<details>
<summary>v1/ConfigMap some-namespace/postSyncHookAndNonDefaultSyncWave</summary>

` + triple + `yaml
apiVersion: v1
kind: ConfigMap
metadata:
  annotations:
    argocd.argoproj.io/hook: PostSync
    argocd.argoproj.io/sync-wave: "5"
  name: postSyncHookAndNonDefaultSyncWave
  namespace: some-namespace
` + triple + `
</details>

<details>
<summary>v1/ConfigMap other-namespace/helmPostInstallHookWithWeight</summary>

` + triple + `yaml
apiVersion: v1
kind: ConfigMap
metadata:
  annotations:
    helm.sh/hook: post-install
    helm.sh/hook-weight: "5"
  name: helmPostInstallHookWithWeight
  namespace: other-namespace
` + triple + `
</details>
</details>
</details>`
	assert.Equal(t, expected, res.Details)
}
