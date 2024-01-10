package pkg

import (
	"context"
	"strings"
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

func TestMultipleItemsWithNewlines(t *testing.T) {
	var (
		message = NewMessage("name", 1, 2)
		ctx     = context.Background()
	)
	message.AddNewApp(ctx, "first-app")
	message.AddToAppMessage(ctx, "first-app", CheckResult{
		State:   StateSuccess,
		Summary: "summary-1",
		Details: "detail-1",
	})
	message.AddToAppMessage(ctx, "first-app", CheckResult{
		State:   StateSuccess,
		Summary: "summary-2",
		Details: "detail-2",
	})
	message.AddNewApp(ctx, "second-app")
	message.AddToAppMessage(ctx, "second-app", CheckResult{
		State:   StateSuccess,
		Summary: "summary-1",
		Details: "detail-1",
	})
	message.AddToAppMessage(ctx, "second-app", CheckResult{
		State:   StateSuccess,
		Summary: "summary-2",
		Details: "detail-2",
	})
	result := message.BuildComment(ctx)

	// header rows need double newlines before and after
	index := 0
	newline := uint8('\n')
	for {
		index++
		foundAt := strings.Index(result[index:], "---")
		if foundAt == -1 {
			break // couldn't be found, we're done
		}
		index += foundAt

		if index < 2 {
			continue // hyphens are at the beginning of the string, we're fine
		}

		if result[index-1] == '-' || result[index+3] == '-' {
			continue // not a triple-hyphen, but a more-than-triple-hyphen, move on
		}

		// must be preceded by two newlines
		assert.Equal(t, newline, result[index-1])
		assert.Equal(t, newline, result[index-2])

		// must be followed by two newlines
		assert.Equal(t, newline, result[index+3])
		assert.Equal(t, newline, result[index+4])
	}
}
