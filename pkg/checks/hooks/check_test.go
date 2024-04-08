package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

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

func toYaml(obj data) string {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	err := enc.Encode(obj)
	if err != nil {
		panic(err)
	}

	text := buf.String()
	return strings.TrimSpace(text)
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
<summary>PreSync phase, wave 0 (1 resource)</summary>

` + triple + `yaml
` + toYaml(preSyncHookAndDefaultSyncWave) + `
` + triple + `

</details>

<details>
<summary>PreSync phase, wave 5 (1 resource)</summary>

` + triple + `yaml
` + toYaml(preSyncHookAndNonDefaultSyncWave) + `
` + triple + `

</details>

<details>
<summary>PostSync phase, wave 0 (1 resource)</summary>

` + triple + `yaml
` + toYaml(helmPostInstallHook) + `
` + triple + `

</details>

<details>
<summary>PostSync phase, wave 5 (2 resources)</summary>

` + triple + `yaml
` + toYaml(postSyncHookAndNonDefaultSyncWave) + `

---

` + toYaml(helmPostInstallHookWithWeight) + `
` + triple + `

</details>`
	assert.Equal(t, expected, res.Details)
}
