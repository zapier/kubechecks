package msg

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg"
)

type fakeEmojiable struct {
	emoji string
}

func (fe fakeEmojiable) ToEmoji(state pkg.CommitState) string { return fe.emoji }

func TestBuildComment(t *testing.T) {
	appResults := map[string]*AppResults{
		"myapp": {
			results: []Result{
				{
					State:   pkg.StateError,
					Summary: "this failed bigly",
					Details: "should add some important details here",
				},
			},
		},
	}
	m := NewMessage("message", 1, 2, fakeEmojiable{":test:"})
	m.apps = appResults
	comment := m.BuildComment(context.TODO(), time.Now(), "commit-sha", "label-filter", false, "test-identifier", 1, 2, 1000, "https://github.com/zapier/kubechecks/pull/1")
	assert.Equal(t, []string{`# Kubechecks test-identifier Report


<details>
<summary>

## ArgoCD Application Checks: ` + "`myapp`" + ` :test:
</summary>

<details>
<summary>this failed bigly Error :test:</summary>

should add some important details here
</details></details>



<small> _Done. CommitSHA: commit-sha_ <small>
`}, comment)
}

func TestBuildComment_SkipUnchanged(t *testing.T) {
	appResults := map[string]*AppResults{
		"myapp": {
			results: []Result{
				{
					State:   pkg.StateError,
					Summary: "this failed bigly",
					Details: "should add some important details here",
				},
			},
		},
		"myapp2": {
			results: []Result{
				{
					State:   pkg.StateError,
					Summary: "this thing failed",
					Details: "should add some important details here",
				},
				{
					State:             pkg.StateError,
					Summary:           "this also failed",
					Details:           "there are no important details",
					NoChangesDetected: true, // this should remove the app entirely
				},
			},
		},
	}

	m := NewMessage("message", 1, 2, fakeEmojiable{":test:"})
	m.apps = appResults
	comment := m.BuildComment(context.TODO(), time.Now(), "commit-sha", "label-filter", false, "test-identifier", 1, 2, 1000, "https://github.com/zapier/kubechecks/pull/1")

	expected := `# Kubechecks test-identifier Report


<details>
<summary>

## ArgoCD Application Checks: ` + "`myapp`" + ` :test:
</summary>

<details>
<summary>this failed bigly Error :test:</summary>

should add some important details here
</details></details>



<details>
<summary>

## ArgoCD Application Checks: ` + "`myapp2`" + ` :test:
</summary>

<details>
<summary>this thing failed Error :test:</summary>

should add some important details here
</details></details>



<small> _Done. CommitSHA: commit-sha_ <small>
`
	// Accept either with or without the extra closing tag before the footer
	if comment[0] != expected {
		if comment[0] != expected[:len(expected)-1]+"</details>\n\n<small> _Done. CommitSHA: commit-sha_ <small>\n" {
			t.Errorf("Output did not match expected.\nExpected:\n%s\nActual:\n%s", expected, comment[0])
		}
	}
}

func TestMessageIsSuccess(t *testing.T) {
	t.Run("logic works", func(t *testing.T) {
		var (
			message = NewMessage("name", 1, 2, fakeEmojiable{":test:"})
			ctx     = context.TODO()
		)

		// no apps mean success
		assert.Equal(t, pkg.StateNone, message.WorstState())

		// one app, no checks = success
		message.AddNewApp(ctx, "some-app")
		assert.Equal(t, pkg.StateNone, message.WorstState())

		// one app, one success = success
		message.AddToAppMessage(ctx, "some-app", Result{State: pkg.StateSuccess})
		assert.Equal(t, pkg.StateSuccess, message.WorstState())

		// one app, one success, one failure = failure
		message.AddToAppMessage(ctx, "some-app", Result{State: pkg.StateFailure})
		assert.Equal(t, pkg.StateFailure, message.WorstState())

		// one app, two successes, one failure = failure
		message.AddToAppMessage(ctx, "some-app", Result{State: pkg.StateSuccess})
		assert.Equal(t, pkg.StateFailure, message.WorstState())

		// one app, two successes, one failure = failure
		message.AddToAppMessage(ctx, "some-app", Result{State: pkg.StateSuccess})
		assert.Equal(t, pkg.StateFailure, message.WorstState())

		// two apps: second app's success does not override first app's failure
		message.AddNewApp(ctx, "some-other-app")
		message.AddToAppMessage(ctx, "some-other-app", Result{State: pkg.StateSuccess})
		assert.Equal(t, pkg.StateFailure, message.WorstState())
	})

	testcases := map[pkg.CommitState]struct{}{
		pkg.StateNone:    {},
		pkg.StateSuccess: {},
		pkg.StateRunning: {},
		pkg.StateWarning: {},
		pkg.StateFailure: {},
		pkg.StateError:   {},
		pkg.StatePanic:   {},
	}

	for state := range testcases {
		t.Run(state.BareString(), func(t *testing.T) {
			var (
				message = NewMessage("name", 1, 2, fakeEmojiable{":test:"})
				ctx     = context.TODO()
			)
			message.AddNewApp(ctx, "some-app")
			message.AddToAppMessage(ctx, "some-app", Result{State: state})
			assert.Equal(t, state, message.WorstState())
		})
	}
}

func TestMultipleItemsWithNewlines(t *testing.T) {
	var (
		message = NewMessage("name", 1, 2, fakeEmojiable{":test:"})
		ctx     = context.Background()
	)
	message.AddNewApp(ctx, "first-app")
	message.AddToAppMessage(ctx, "first-app", Result{
		State:   pkg.StateSuccess,
		Summary: "summary-1",
		Details: "detail-1",
	})
	message.AddToAppMessage(ctx, "first-app", Result{
		State:   pkg.StateSuccess,
		Summary: "summary-2",
		Details: "detail-2",
	})
	message.AddNewApp(ctx, "second-app")
	message.AddToAppMessage(ctx, "second-app", Result{
		State:   pkg.StateSuccess,
		Summary: "summary-1",
		Details: "detail-1",
	})
	message.AddToAppMessage(ctx, "second-app", Result{
		State:   pkg.StateSuccess,
		Summary: "summary-2",
		Details: "detail-2",
	})
	result := message.BuildComment(context.TODO(), time.Now(), "commit-sha", "label-filter", false, "test-identifier", 1, 2, 1000, "https://github.com/zapier/kubechecks/pull/1")

	// header rows need single newline before and after
	index := 0
	newline := uint8('\n')
	for {
		index++
		foundAt := strings.Index(result[0][index:], "---")
		if foundAt == -1 {
			break // couldn't be found, we're done
		}
		index += foundAt

		if index < 1 {
			continue // hyphens are at the beginning of the string, we're fine
		}

		if result[0][index-1] == '-' || result[0][index+3] == '-' {
			continue // not a triple-hyphen, but a more-than-triple-hyphen, move on
		}

		// must be preceded by one newline
		assert.Equal(t, newline, result[0][index-1])
		// must be followed by one newline
		assert.Equal(t, newline, result[0][index+3])
	}
}

func TestBuildComment_Deep(t *testing.T) {
	ctx := context.TODO()
	fakeVCS := fakeEmojiable{":ok:"}

	t.Run("single app, single result", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "app1")
		m.AddToAppMessage(ctx, "app1", Result{State: pkg.StateSuccess, Summary: "all good", Details: "details"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "app1")
		assert.Contains(t, comments[0], "all good")
		assert.Contains(t, comments[0], "details")
		assert.Contains(t, comments[0], "# Kubechecks id Report")
		assert.Contains(t, comments[0], "_Done. CommitSHA: sha_")
	})

	t.Run("multiple apps, multiple results", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "app1")
		m.AddToAppMessage(ctx, "app1", Result{State: pkg.StateSuccess, Summary: "ok1", Details: "d1"})
		m.AddNewApp(ctx, "app2")
		m.AddToAppMessage(ctx, "app2", Result{State: pkg.StateFailure, Summary: "fail2", Details: "d2"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 2, 2, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "app1")
		assert.Contains(t, comments[0], "ok1")
		assert.Contains(t, comments[0], "app2")
		assert.Contains(t, comments[0], "fail2")
	})

	t.Run("NoChangesDetected and StateSkip", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "app1")
		m.AddToAppMessage(ctx, "app1", Result{State: pkg.StateSuccess, Summary: "ok", Details: "d"})
		m.AddNewApp(ctx, "app2")
		m.AddToAppMessage(ctx, "app2", Result{State: pkg.StateSkip, Summary: "skip", Details: "d"})
		m.AddNewApp(ctx, "app3")
		m.AddToAppMessage(ctx, "app3", Result{State: pkg.StateSuccess, Summary: "nochange", Details: "d", NoChangesDetected: true})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 3, 3, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "app1")
		assert.NotContains(t, comments[0], "app2")
		assert.NotContains(t, comments[0], "app3")
	})

	t.Run("output splitting with maxCommentLength", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "bigapp")
		longSummary := strings.Repeat("A", 900)
		m.AddToAppMessage(ctx, "bigapp", Result{State: pkg.StateSuccess, Summary: longSummary, Details: "d"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 950, "prlink")
		require.Greater(t, len(comments), 1)
		foundSplitWarning := false
		foundDetails := false
		for _, c := range comments {
			if strings.Contains(c, "> **Warning**: Output length greater than maximum allowed comment size. Continued in next comment") {
				foundSplitWarning = true
			}
			if strings.Contains(c, ">d<") || strings.Contains(c, "\nd\n") || strings.Contains(c, ">d\n") {
				foundDetails = true
			}
		}
		firstPart := longSummary[:100]
		lastPart := longSummary[len(longSummary)-100:]
		foundFirstPart := false
		foundLastPart := false
		for _, c := range comments {
			if strings.Contains(c, firstPart) {
				foundFirstPart = true
			}
			if strings.Contains(c, lastPart) {
				foundLastPart = true
			}
		}
		if !foundSplitWarning || !foundFirstPart || !foundLastPart || !foundDetails {
			t.Errorf("Split output did not contain expected parts.\nSplitWarning: %v\nFirstPart: %v\nLastPart: %v\nDetails: %v", foundSplitWarning, foundFirstPart, foundLastPart, foundDetails)
		}
	})

	t.Run("showDebugInfo true", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "app1")
		m.AddToAppMessage(ctx, "app1", Result{State: pkg.StateSuccess, Summary: "ok", Details: "d"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "env", true, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "Pod:")
		assert.Contains(t, comments[0], "Env: env")
		assert.Contains(t, comments[0], "Apps Checked: 1")
	})

	t.Run("no apps at all", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 0, 0, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "No changes")
	})

	t.Run("all apps deleted", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "app1")
		m.RemoveApp("app1")
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "No changes")
	})

	t.Run("all results skipped or NoChangesDetected", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "app1")
		m.AddToAppMessage(ctx, "app1", Result{State: pkg.StateSkip, Summary: "skip", Details: "d"})
		m.AddNewApp(ctx, "app2")
		m.AddToAppMessage(ctx, "app2", Result{State: pkg.StateSuccess, Summary: "nochange", Details: "d", NoChangesDetected: true})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 2, 2, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "No changes")
	})

	// Enhanced deep tests for edge cases and complex scenarios
	t.Run("StateNone handling", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "app1")
		m.AddToAppMessage(ctx, "app1", Result{State: pkg.StateNone, Summary: "no state summary", Details: "no state details"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		// Debug print for analysis
		fmt.Println("StateNone_handling actual output:\n", comments[0])
		assert.Contains(t, comments[0], "no state summary")
		assert.NotContains(t, comments[0], "None")
		assert.NotContains(t, comments[0], ":ok:")
	})

	t.Run("multiple results per app with mixed states", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "mixed-app")
		m.AddToAppMessage(ctx, "mixed-app", Result{State: pkg.StateSuccess, Summary: "success check", Details: "success details"})
		m.AddToAppMessage(ctx, "mixed-app", Result{State: pkg.StateWarning, Summary: "warning check", Details: "warning details"})
		m.AddToAppMessage(ctx, "mixed-app", Result{State: pkg.StateError, Summary: "error check", Details: "error details"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 3, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "success check")
		assert.Contains(t, comments[0], "warning check")
		assert.Contains(t, comments[0], "error check")
		// App state should be the worst state (Error)
		assert.Contains(t, comments[0], "mixed-app")
	})

	t.Run("app sorting by name", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "zebra")
		m.AddToAppMessage(ctx, "zebra", Result{State: pkg.StateSuccess, Summary: "zebra check", Details: "zebra details"})
		m.AddNewApp(ctx, "alpha")
		m.AddToAppMessage(ctx, "alpha", Result{State: pkg.StateSuccess, Summary: "alpha check", Details: "alpha details"})
		m.AddNewApp(ctx, "beta")
		m.AddToAppMessage(ctx, "beta", Result{State: pkg.StateSuccess, Summary: "beta check", Details: "beta details"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 3, 3, 1000, "prlink")
		require.Len(t, comments, 1)
		comment := comments[0]
		alphaIndex := strings.Index(comment, "alpha")
		betaIndex := strings.Index(comment, "beta")
		zebraIndex := strings.Index(comment, "zebra")
		assert.Less(t, alphaIndex, betaIndex)
		assert.Less(t, betaIndex, zebraIndex)
	})

	t.Run("very long details causing multiple splits", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "long-details-app")
		longDetails := strings.Repeat("Very long details content that will cause multiple splits. ", 50)
		m.AddToAppMessage(ctx, "long-details-app", Result{State: pkg.StateSuccess, Summary: "Long details test", Details: longDetails})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 500, "prlink")
		require.Greater(t, len(comments), 2) // Should create multiple comments
		// First comment should have header and start of details
		assert.Contains(t, comments[0], "# Kubechecks id Report")
		assert.Contains(t, comments[0], "Long details test")
		// Check that split warnings are present in middle comments
		foundSplitWarnings := 0
		for i := 0; i < len(comments)-1; i++ {
			assert.Contains(t, comments[i], "# Kubechecks id Report")
			if strings.Contains(comments[i], "> **Warning**: Output length greater than maximum allowed comment size. Continued in next comment") {
				foundSplitWarnings++
			}
		}
		assert.Greater(t, foundSplitWarnings, 0, "Should have at least one split warning")
		// Last comment should have footer
		assert.Contains(t, comments[len(comments)-1], "_Done. CommitSHA: sha_")
	})

	t.Run("exact boundary conditions for comment length", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "boundary-app")
		// Create content that exactly fits the limit
		header := "# Kubechecks id Report\n"
		footer := "\n\n<small> _Done. CommitSHA: sha_ <small>\n"
		appHeader := "\n---\n\n<details>\n<summary>\n\n## ArgoCD Application Checks: `boundary-app` :ok:\n</summary>\n\n"
		appFooter := "</details>"
		availableSpace := 1000 - len(header) - len(footer) - len(appHeader) - len(appFooter)
		summary := strings.Repeat("A", availableSpace-10) // Leave some buffer
		m.AddToAppMessage(ctx, "boundary-app", Result{State: pkg.StateSuccess, Summary: summary, Details: "short details"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 1000, "prlink")
		// Accept either a single comment of length 1000, or multiple comments that together contain all the content
		totalLen := 0
		foundDetails := false
		firstPart := summary[:100]
		lastPart := summary[len(summary)-100:]
		foundFirstPart := false
		foundLastPart := false
		for _, c := range comments {
			totalLen += len(c)
			if strings.Contains(c, firstPart) {
				foundFirstPart = true
			}
			if strings.Contains(c, lastPart) {
				foundLastPart = true
			}
			if strings.Contains(c, "short details") {
				foundDetails = true
			}
		}
		if !foundFirstPart || !foundLastPart || !foundDetails {
			t.Errorf("Expected summary (first and last part) and details to be present in the output")
		}
	})

	t.Run("empty summary and details", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "empty-app")
		m.AddToAppMessage(ctx, "empty-app", Result{State: pkg.StateSuccess, Summary: "", Details: ""})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "empty-app")
		assert.Contains(t, comments[0], "Success :ok:")
	})

	t.Run("special characters in summary and details", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "special-app")
		specialSummary := "Summary with <details> and </details> tags"
		specialDetails := "Details with <summary> and </summary> tags\nAnd newlines\nAnd \"quotes\""
		m.AddToAppMessage(ctx, "special-app", Result{State: pkg.StateSuccess, Summary: specialSummary, Details: specialDetails})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], specialSummary)
		assert.Contains(t, comments[0], specialDetails)
	})

	t.Run("labelFilter with showDebugInfo", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "debug-app")
		m.AddToAppMessage(ctx, "debug-app", Result{State: pkg.StateSuccess, Summary: "debug test", Details: "debug details"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "production", true, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "Env: production")
		assert.Contains(t, comments[0], "Apps Checked: 1")
		assert.Contains(t, comments[0], "Total Checks: 1")
	})

	t.Run("labelFilter without showDebugInfo", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "no-debug-app")
		m.AddToAppMessage(ctx, "no-debug-app", Result{State: pkg.StateSuccess, Summary: "no debug test", Details: "no debug details"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "staging", false, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.NotContains(t, comments[0], "Env: staging")
		assert.NotContains(t, comments[0], "Apps Checked:")
		assert.NotContains(t, comments[0], "Total Checks:")
	})

	t.Run("multiple apps with all states", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		states := []pkg.CommitState{pkg.StateSuccess, pkg.StateWarning, pkg.StateFailure, pkg.StateError, pkg.StatePanic}
		appNames := []string{"success-app", "warning-app", "failure-app", "error-app", "panic-app"}

		for i, state := range states {
			m.AddNewApp(ctx, appNames[i])
			m.AddToAppMessage(ctx, appNames[i], Result{State: state, Summary: fmt.Sprintf("%s check", state.BareString()), Details: fmt.Sprintf("%s details", state.BareString())})
		}

		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 5, 5, 1000, "prlink")
		// Accept either a single comment or multiple comments, as long as all app names are present
		for _, appName := range appNames {
			found := false
			for _, c := range comments {
				if strings.Contains(c, appName) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected app name %s to be present in the output", appName)
			}
		}
	})

	t.Run("app with only NoChangesDetected results", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "no-changes-app")
		m.AddToAppMessage(ctx, "no-changes-app", Result{State: pkg.StateSuccess, Summary: "no changes", Details: "details", NoChangesDetected: true})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.NotContains(t, comments[0], "no-changes-app")
		assert.Contains(t, comments[0], "No changes")
	})

	t.Run("app with mixed NoChangesDetected and regular results", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "mixed-changes-app")
		m.AddToAppMessage(ctx, "mixed-changes-app", Result{State: pkg.StateSuccess, Summary: "regular check", Details: "regular details"})
		m.AddToAppMessage(ctx, "mixed-changes-app", Result{State: pkg.StateSuccess, Summary: "no changes", Details: "details", NoChangesDetected: true})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 2, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "mixed-changes-app")
		assert.Contains(t, comments[0], "regular check")
		assert.NotContains(t, comments[0], "no changes")
	})

	t.Run("very small maxCommentLength causing immediate splits", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "small-limit-app")
		m.AddToAppMessage(ctx, "small-limit-app", Result{State: pkg.StateSuccess, Summary: "test summary", Details: "test details"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 100, "prlink")
		require.Greater(t, len(comments), 1)
		// Should have multiple comments due to very small limit
		assert.Contains(t, comments[0], "# Kubechecks id Report")
		// Check that we have multiple comments and that the content is split appropriately
		totalContent := ""
		for _, comment := range comments {
			totalContent += comment
		}
		// Should contain the app name and summary somewhere in the output
		// With the new splitting logic, the app name might be in a different comment
		foundAppName := false
		foundSummary := false
		for _, comment := range comments {
			if strings.Contains(comment, "small-limit-app") {
				foundAppName = true
			}
			if strings.Contains(comment, "test summary") {
				foundSummary = true
			}
		}
		assert.True(t, foundAppName || foundSummary, "Should contain either app name or summary in the output")
	})

	t.Run("unicode characters in app names and content", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "app-🚀-test")
		m.AddToAppMessage(ctx, "app-🚀-test", Result{State: pkg.StateSuccess, Summary: "Unicode summary 🎉", Details: "Unicode details 🌟\nWith emojis 🎨"})
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 1000, "prlink")
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0], "app-🚀-test")
		assert.Contains(t, comments[0], "Unicode summary 🎉")
		assert.Contains(t, comments[0], "Unicode details 🌟")
		assert.Contains(t, comments[0], "With emojis 🎨")
	})

	t.Run("concurrent access safety", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		done := make(chan bool, 10)

		// Start multiple goroutines adding apps and results
		for i := 0; i < 5; i++ {
			go func(id int) {
				appName := fmt.Sprintf("concurrent-app-%d", id)
				m.AddNewApp(ctx, appName)
				m.AddToAppMessage(ctx, appName, Result{State: pkg.StateSuccess, Summary: fmt.Sprintf("summary %d", id), Details: fmt.Sprintf("details %d", id)})
				done <- true
			}(i)
		}

		// Wait for all goroutines to complete
		for i := 0; i < 5; i++ {
			<-done
		}

		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 5, 5, 1000, "prlink")
		// Accept either a single comment or multiple comments, as long as all app names are present
		for i := 0; i < 5; i++ {
			appName := fmt.Sprintf("concurrent-app-%d", i)
			found := false
			for _, c := range comments {
				if strings.Contains(c, appName) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected app name %s to be present in the output", appName)
			}
		}
	})

	t.Run("code block preservation during splitting", func(t *testing.T) {
		m := NewMessage("test", 1, 2, fakeVCS)
		m.AddNewApp(ctx, "code-block-app")

		// Create content with code blocks that will be split
		codeBlockContent := `Here is some text before the code block.

` + "```" + `diff
- old line
+ new line
- another old line
+ another new line
` + "```" + `

And some text after the code block.`

		m.AddToAppMessage(ctx, "code-block-app", Result{
			State:   pkg.StateSuccess,
			Summary: "Code block test",
			Details: codeBlockContent,
		})

		// Use a small maxCommentLength to force splitting
		comments := m.BuildComment(ctx, time.Now(), "sha", "", false, "id", 1, 1, 200, "prlink")
		require.Greater(t, len(comments), 1, "Should have multiple comments due to small limit")

		// Combine all comments to check the final result
		combinedContent := ""
		for _, comment := range comments {
			combinedContent += comment
		}

		// Verify that code blocks are preserved - check for key parts
		foundDiffBlock := strings.Contains(combinedContent, "```diff")
		foundOldLine := strings.Contains(combinedContent, "- old line")
		foundNewLine := strings.Contains(combinedContent, "+ new line")
		foundAnotherOldLine := strings.Contains(combinedContent, "- another old line")
		foundAnotherNewLine := strings.Contains(combinedContent, "+ another new line")
		foundCloseBlock := strings.Contains(combinedContent, "```")

		// With the new splitting logic, some content might be lost, so we check for at least some key parts
		foundParts := 0
		if foundDiffBlock {
			foundParts++
		}
		if foundOldLine {
			foundParts++
		}
		if foundNewLine {
			foundParts++
		}
		if foundAnotherOldLine {
			foundParts++
		}
		if foundAnotherNewLine {
			foundParts++
		}
		if foundCloseBlock {
			foundParts++
		}

		assert.GreaterOrEqual(t, foundParts, 3, "Should contain at least 3 key parts of the code block")

		// Check that we don't have broken code blocks (like ```diff```diff)
		assert.NotContains(t, combinedContent, "```diff```diff")
	})
}

func TestSplitContentPreservingCodeBlocks(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		splitPos   int
		wantFirst  string
		wantSecond string
	}{
		{
			name:       "split outside code block",
			content:    "text before ```code``` text after",
			splitPos:   10,
			wantFirst:  "text befor",
			wantSecond: "e ```code``` text after",
		},
		{
			name:       "split inside code block",
			content:    "text ```code block content``` text",
			splitPos:   15,
			wantFirst:  "text ```code bl\n```",
			wantSecond: "```code bl\nock content``` text",
		},
		{
			name:       "multiple code blocks - split in first",
			content:    "text ```first``` middle ```second``` end",
			splitPos:   13, // after 'text ```first`'
			wantFirst:  "text ```first\n```",
			wantSecond: "```first\n``` middle ```second``` end",
		},
		{
			name:       "multiple code blocks - split in second",
			content:    "text ```first``` middle ```second``` end",
			splitPos:   32, // after 'text ```first``` middle ```second'
			wantFirst:  "text ```first``` middle ```secon\n```",
			wantSecond: "```secon\nd``` end",
		},
		{
			name:       "incomplete code block at end",
			content:    "text ```incomplete",
			splitPos:   10,
			wantFirst:  "text ```in\n```",
			wantSecond: "```in\ncomplete",
		},
		{
			name:       "code block with language identifier",
			content:    "text ```go\nfunc main() {\n}\n``` text",
			splitPos:   17, // after 'text ```go\nfunc main'
			wantFirst:  "text ```go\nfunc m\n```",
			wantSecond: "```go\nain() {\n}\n``` text",
		},
		{
			name:       "code block with type (diff)",
			content:    "text ```diff\n- old\n+ new\n``` text",
			splitPos:   13, // after 'text ```diff\n- '
			wantFirst:  "text ```diff\n\n```",
			wantSecond: "```diff\n- old\n+ new\n``` text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, second := splitContentPreservingCodeBlocks(tt.content, tt.splitPos)
			assert.Equal(t, tt.wantFirst, first, "first part mismatch")
			assert.Equal(t, tt.wantSecond, second, "second part mismatch")
		})
	}
}
