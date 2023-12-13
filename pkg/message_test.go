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

func TestMessageIsSuccess(t *testing.T) {
	t.Run("logic works", func(t *testing.T) {
		var (
			message = NewMessage("name", 1, 2)
			ctx     = context.TODO()
		)

		// no apps mean success
		assert.True(t, message.IsSuccess())

		// one app, no checks = success
		message.AddNewApp(ctx, "some-app")
		assert.True(t, message.IsSuccess())

		// one app, one success = success
		message.AddToAppMessage(ctx, "some-app", CheckResult{State: StateSuccess})
		assert.True(t, message.IsSuccess())

		// one app, one success, one failure = failure
		message.AddToAppMessage(ctx, "some-app", CheckResult{State: StateFailure})
		assert.False(t, message.IsSuccess())

		// one app, two successes, one failure = failure
		message.AddToAppMessage(ctx, "some-app", CheckResult{State: StateSuccess})
		assert.False(t, message.IsSuccess())

		// one app, two successes, one failure = failure
		message.AddToAppMessage(ctx, "some-app", CheckResult{State: StateSuccess})
		assert.False(t, message.IsSuccess())

		// two apps: second app's success does not override first app's failure
		message.AddNewApp(ctx, "some-other-app")
		message.AddToAppMessage(ctx, "some-other-app", CheckResult{State: StateSuccess})
		assert.False(t, message.IsSuccess())
	})

	testcases := map[CommitState]bool{
		StateNone:    true,
		StateSuccess: true,
		StateRunning: true,
		StateWarning: false,
		StateFailure: false,
		StateError:   false,
		StatePanic:   false,
	}

	for state, expected := range testcases {
		t.Run(state.BareString(), func(t *testing.T) {
			var (
				message = NewMessage("name", 1, 2)
				ctx     = context.TODO()
			)
			message.AddNewApp(ctx, "some-app")
			message.AddToAppMessage(ctx, "some-app", CheckResult{State: state})
			assert.Equal(t, expected, message.IsSuccess())
		})
	}
}
