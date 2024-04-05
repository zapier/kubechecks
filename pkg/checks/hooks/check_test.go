package hooks

import (
	"context"
	"encoding/json"
	"fmt"
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
	return text
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

	req := checks.Request{
		JsonManifests: []string{
			toJson(preSyncHookAndDefaultSyncWave),
			toJson(preSyncHookAndNonDefaultSyncWave),
			toJson(postSyncHookAndNonDefaultSyncWave),
		},
	}

	triple := "```"

	res, err := Check(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, pkg.StateNone, res.State)
	assert.Equal(t, "<b>Sync Phases: PreSync, PostSync</b>", res.Summary)
	assert.Equal(t, fmt.Sprintf(`<details>
<summary>PreSync phase, wave 0 (1 resource)</summary>

`+triple+`diff
===== v1/ConfigMap some-namespace/preSyncHookAndDefaultSyncWave =====

%s

`+triple+`

</details>

<details>
<summary>PreSync phase, wave 5 (1 resource)</summary>

`+triple+`diff
===== v1/ConfigMap some-namespace/preSyncHookAndNonDefaultSyncWave =====

%s

`+triple+`

</details>

<details>
<summary>PostSync phase, wave 5 (1 resource)</summary>

`+triple+`diff
===== v1/ConfigMap some-namespace/postSyncHookAndNonDefaultSyncWave =====

%s

`+triple+`

</details>`, toYaml(preSyncHookAndDefaultSyncWave), toYaml(preSyncHookAndNonDefaultSyncWave), toYaml(postSyncHookAndNonDefaultSyncWave)), res.Details)
}
