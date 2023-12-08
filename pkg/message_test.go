package pkg

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildComment(t *testing.T) {
	appResults := map[string]*AppResults{
		"myapp": {
			results: []CheckResult{
				{
					State:   StateError,
					Summary: "this failed bigly",
					Details: "should add some important details here",
				},
			},
		},
	}
	comment := buildComment(context.TODO(), appResults)
	assert.Equal(t, `# Kubechecks Report
<details>
<summary>

## ArgoCD Application Checks: `+"`myapp`"+` :heavy_exclamation_mark:
</summary>

<details>
<summary>this failed bigly Error :heavy_exclamation_mark:</summary>

should add some important details here
</details></details>`, comment)
}
