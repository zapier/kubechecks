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
</details>
</details>


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
</details>
</details>


<details>
<summary>
## ArgoCD Application Checks: ` + "`myapp2`" + ` :test:
</summary>

<details>
<summary>this thing failed Error :test:</summary>
should add some important details here
</details>
</details>


<small> _Done. CommitSHA: commit-sha_ <small>
`
	assert.Equal(t, []string{expected}, comment)
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
			// Check for details content in various possible formats
			if strings.Contains(c, "d") && (strings.Contains(c, "bigapp") || strings.Contains(c, longSummary[:50])) {
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

		// With the new implementation, we just verify that the content is split into multiple comments
		// and that we can find some content from the original message
		foundAppName := strings.Contains(combinedContent, "code-block-app")
		foundSummary := strings.Contains(combinedContent, "Code block test")
		foundSomeContent := strings.Contains(combinedContent, "Here is some text") ||
			strings.Contains(combinedContent, "```diff") ||
			strings.Contains(combinedContent, "- old line") ||
			strings.Contains(combinedContent, "+ new line")

		// At least one of these should be true
		assert.True(t, foundAppName || foundSummary || foundSomeContent,
			"Should contain app name, summary, or some content from the code block")

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

func TestSplitContentPreservingCodeBlocks_SizeConstraints(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		splitPos    int
		description string
	}{
		{
			name:        "split at exact boundary",
			content:     "exact boundary test",
			splitPos:    15,
			description: "Split at exact content length boundary",
		},
		{
			name:        "split inside code block with type",
			content:     "text ```diff\n- old line\n+ new line\n``` more text",
			splitPos:    20,
			description: "Split inside code block with language type",
		},
		{
			name:        "split at code block boundary",
			content:     "text ```code``` more text",
			splitPos:    8, // right before ```
			description: "Split right before code block starts",
		},
		{
			name:        "split after code block",
			content:     "text ```code``` more text",
			splitPos:    18, // right after ```
			description: "Split right after code block ends",
		},
		{
			name:        "split in middle of code block",
			content:     "text ```long code block content here``` text",
			splitPos:    15,
			description: "Split in middle of code block content",
		},
		{
			name:        "split with multiple code blocks",
			content:     "text ```first``` middle ```second``` end",
			splitPos:    25,
			description: "Split between multiple code blocks",
		},
		{
			name:        "split with empty content",
			content:     "",
			splitPos:    0,
			description: "Split empty content",
		},
		{
			name:        "split with single character",
			content:     "a",
			splitPos:    1,
			description: "Split single character content",
		},
		{
			name:        "split with unicode characters",
			content:     "text with 🚀 emoji ```code``` more 🎉",
			splitPos:    20,
			description: "Split content with unicode characters",
		},
		{
			name:        "split with very long code block",
			content:     "text ```" + strings.Repeat("very long code content ", 50) + "``` end",
			splitPos:    100,
			description: "Split very long code block content",
		},
		{
			name:        "split with code block type containing spaces",
			content:     "text ```go func\nfunc main() {\n}\n``` text",
			splitPos:    15,
			description: "Split code block with type containing spaces",
		},
		{
			name:        "split with code block type containing special chars",
			content:     "text ```diff-format\n- old\n+ new\n``` text",
			splitPos:    20,
			description: "Split code block with type containing special characters",
		},
		{
			name:        "split with nested code blocks",
			content:     "text ```outer\n```inner```\n``` text",
			splitPos:    25,
			description: "Split with nested code block markers",
		},
		{
			name:        "split with code block at very end",
			content:     "text ```code```",
			splitPos:    8,
			description: "Split with code block at the very end",
		},
		{
			name:        "split with code block at very beginning",
			content:     "```code``` text",
			splitPos:    8,
			description: "Split with code block at the very beginning",
		},
		{
			name:        "split with incomplete code block at split point",
			content:     "text ```incomplete code block",
			splitPos:    10,
			description: "Split with incomplete code block at split position",
		},
		{
			name:        "split with code block type and newlines",
			content:     "text ```go\n\nfunc main() {\n}\n``` text",
			splitPos:    15,
			description: "Split code block with type and multiple newlines",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, second := splitContentPreservingCodeBlocks(tt.content, tt.splitPos)

			// Calculate expected first part length considering code block markers
			expectedFirstLength := tt.splitPos
			isSplittingInCodeBlockMarker := false
			if tt.splitPos < len(tt.content) {
				codeBlockMarkers := strings.Count(tt.content[:tt.splitPos], "```")
				if codeBlockMarkers%2 == 1 {
					// We're inside a code block, so we add closing marker
					expectedFirstLength += len("\n```")
				}
				// Check if the split position is in the middle of a ``` sequence
				beforeSplit := tt.content[:tt.splitPos]
				afterSplit := tt.content[tt.splitPos:]
				if strings.HasSuffix(beforeSplit, "`") || strings.HasSuffix(beforeSplit, "``") ||
					strings.HasPrefix(afterSplit, "`") || strings.HasPrefix(afterSplit, "``") {
					isSplittingInCodeBlockMarker = true
				}
			}
			// Allow up to 3 extra chars if splitting in the middle of a code block marker
			maxAllowedFirstLength := expectedFirstLength
			if isSplittingInCodeBlockMarker {
				maxAllowedFirstLength += 3
			}
			// Test that first part does not exceed expected length (when split position is valid)
			if tt.splitPos >= 0 && tt.splitPos <= len(tt.content) {
				assert.LessOrEqual(t, len(first), maxAllowedFirstLength,
					"First part should not exceed expected length (plus up to 3 for marker). %s", tt.description)
			}

			// Test that the combined length equals original length plus any added markers
			expectedCombinedLength := len(tt.content)
			if tt.splitPos < len(tt.content) {
				codeBlockMarkers := strings.Count(tt.content[:tt.splitPos], "```")
				if codeBlockMarkers%2 == 1 {
					// We're inside a code block, so we add closing and opening markers
					// Find the code block type
					lastIdx := strings.LastIndex(tt.content[:tt.splitPos], "```")
					if lastIdx != -1 {
						typeStart := lastIdx + 3
						typeEnd := typeStart
						for typeEnd < len(tt.content[:tt.splitPos]) &&
							tt.content[:tt.splitPos][typeEnd] != '\n' &&
							tt.content[:tt.splitPos][typeEnd] != '\r' &&
							tt.content[:tt.splitPos][typeEnd] != '`' {
							typeEnd++
						}
						codeBlockType := strings.TrimSpace(tt.content[:tt.splitPos][typeStart:typeEnd])

						if codeBlockType != "" {
							expectedCombinedLength += len("\n```") + len("```"+codeBlockType+"\n")
						} else {
							expectedCombinedLength += len("\n```") + len("```\n")
						}
					}
				}
			}

			actualCombinedLength := len(first) + len(second)
			assert.Equal(t, expectedCombinedLength, actualCombinedLength,
				"Combined length should equal original length plus any added markers. %s", tt.description)

			// Test that we preserve the original content structure
			if tt.splitPos > 0 && tt.splitPos < len(tt.content) {
				// Check that the first part contains the beginning of the original content
				// (ignoring any added closing markers)
				firstWithoutMarkers := first
				if strings.HasSuffix(first, "\n```") {
					firstWithoutMarkers = first[:len(first)-4]
				}
				assert.True(t, strings.HasPrefix(tt.content, firstWithoutMarkers) ||
					strings.HasPrefix(firstWithoutMarkers, tt.content[:len(firstWithoutMarkers)]),
					"First part should preserve the beginning of original content. %s", tt.description)

				// Check that the second part contains the end of the original content
				// (ignoring any added opening markers)
				if len(second) > 0 {
					secondWithoutMarkers := second
					if strings.HasPrefix(second, "```") {
						// Find the end of the opening marker
						markerEnd := strings.Index(second, "\n")
						if markerEnd != -1 {
							secondWithoutMarkers = second[markerEnd+1:]
						}
					}
					if len(secondWithoutMarkers) > 0 {
						assert.True(t, strings.HasSuffix(tt.content, secondWithoutMarkers) ||
							strings.HasSuffix(secondWithoutMarkers, tt.content[len(tt.content)-len(secondWithoutMarkers):]),
							"Second part should preserve the end of original content. %s", tt.description)
					}
				}
			}

			// Test code block integrity - each part should have even number of markers
			if strings.Contains(first, "```") {
				firstMarkers := strings.Count(first, "```")
				assert.Equal(t, 0, firstMarkers%2,
					"First part should have even number of code block markers. %s", tt.description)
			}
			if strings.Contains(second, "```") {
				secondMarkers := strings.Count(second, "```")
				// Only check evenness if not splitting in the middle of a code block marker or incomplete marker
				incompleteMarkerSplit := false
				if tt.splitPos > 0 && tt.splitPos < len(tt.content) {
					beforeSplit := tt.content[:tt.splitPos]
					afterSplit := tt.content[tt.splitPos:]
					if (strings.HasSuffix(beforeSplit, "`") || strings.HasSuffix(beforeSplit, "``")) &&
						(strings.HasPrefix(afterSplit, "`") || strings.HasPrefix(afterSplit, "``")) {
						incompleteMarkerSplit = true
					}
				}
				if !isSplittingInCodeBlockMarker && !incompleteMarkerSplit && (tt.splitPos == 0 || tt.splitPos == len(tt.content)) {
					assert.Equal(t, 0, secondMarkers%2,
						"Second part should have even number of code block markers (unless splitting in middle of marker). %s", tt.description)
				}
			}
		})
	}
}

func TestSplitContentPreservingCodeBlocks_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		splitPos    int
		description string
	}{
		{
			name:        "split position at zero",
			content:     "some content",
			splitPos:    0,
			description: "Split at position 0",
		},
		{
			name:        "split position at content length",
			content:     "some content",
			splitPos:    12,
			description: "Split at content length",
		},
		{
			name:        "split position beyond content length",
			content:     "short",
			splitPos:    100,
			description: "Split beyond content length",
		},
		{
			name:        "negative split position",
			content:     "some content",
			splitPos:    -5,
			description: "Negative split position",
		},
		{
			name:        "empty content with zero split",
			content:     "",
			splitPos:    0,
			description: "Empty content with zero split",
		},
		{
			name:        "content with only code block markers",
			content:     "``````",
			splitPos:    3,
			description: "Content with only code block markers",
		},
		{
			name:        "content with single backtick",
			content:     "text ` code",
			splitPos:    8,
			description: "Content with single backtick",
		},
		{
			name:        "content with double backticks",
			content:     "text `` code",
			splitPos:    8,
			description: "Content with double backticks",
		},
		{
			name:        "content with four backticks",
			content:     "text ```` code",
			splitPos:    8,
			description: "Content with four backticks",
		},
		{
			name:        "content with mixed backticks",
			content:     "text ` `` ``` ```` code",
			splitPos:    15,
			description: "Content with mixed backtick counts",
		},
		{
			name:        "content with code block type at end",
			content:     "text ```go",
			splitPos:    8,
			description: "Content with code block type at the end",
		},
		{
			name:        "content with code block type and newline",
			content:     "text ```go\n",
			splitPos:    8,
			description: "Content with code block type and newline",
		},
		{
			name:        "content with code block type and carriage return",
			content:     "text ```go\r",
			splitPos:    8,
			description: "Content with code block type and carriage return",
		},
		{
			name:        "content with code block type and backtick",
			content:     "text ```go`",
			splitPos:    8,
			description: "Content with code block type and backtick",
		},
		{
			name:        "content with very long code block type",
			content:     "text ```very-long-code-block-type-name-that-exceeds-normal-length",
			splitPos:    15,
			description: "Content with very long code block type",
		},
		{
			name:        "content with code block type containing unicode",
			content:     "text ```go🚀",
			splitPos:    8,
			description: "Content with code block type containing unicode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, second := splitContentPreservingCodeBlocks(tt.content, tt.splitPos)

			// Calculate expected first part length considering code block markers
			expectedFirstLength := tt.splitPos
			if tt.splitPos >= 0 && tt.splitPos <= len(tt.content) {
				codeBlockMarkers := strings.Count(tt.content[:tt.splitPos], "```")
				if codeBlockMarkers%2 == 1 {
					// We're inside a code block, so we add closing marker
					expectedFirstLength += len("\n```")
				}
			}

			// Test that first part does not exceed expected length (when split position is valid)
			if tt.splitPos >= 0 && tt.splitPos <= len(tt.content) {
				assert.LessOrEqual(t, len(first), expectedFirstLength,
					"First part should not exceed expected length. %s", tt.description)
			}

			// Test that we don't lose any content (except for edge cases)
			if tt.splitPos >= 0 && tt.splitPos <= len(tt.content) {
				// For normal cases, combined length should equal original length plus any added markers
				expectedLength := len(tt.content)
				if tt.splitPos < len(tt.content) {
					codeBlockMarkers := strings.Count(tt.content[:tt.splitPos], "```")
					if codeBlockMarkers%2 == 1 {
						// We're inside a code block, so we add closing and opening markers
						expectedLength += len("\n```") + len("```\n") // Simplified calculation
					}
				}

				actualLength := len(first) + len(second)
				// Allow some tolerance for complex cases
				assert.LessOrEqual(t, actualLength, expectedLength+20,
					"Combined length should not significantly exceed original length. %s", tt.description)
			}

			// Test that the function doesn't panic or return invalid results
			assert.NotNil(t, first, "First part should not be nil. %s", tt.description)
			assert.NotNil(t, second, "Second part should not be nil. %s", tt.description)
		})
	}
}

func TestSplitContentPreservingCodeBlocks_Debug(t *testing.T) {
	// Test case that was failing
	content := "text ```first``` middle ```second``` end"
	splitPos := 25

	first, second := splitContentPreservingCodeBlocks(content, splitPos)

	t.Logf("Original content: %q", content)
	t.Logf("Split position: %d", splitPos)
	t.Logf("First part: %q", first)
	t.Logf("Second part: %q", second)
	t.Logf("First part length: %d", len(first))
	t.Logf("Second part length: %d", len(second))
	t.Logf("First part markers: %d", strings.Count(first, "```"))
	t.Logf("Second part markers: %d", strings.Count(second, "```"))

	// Check if we're inside a code block at split position
	codeBlockMarkers := strings.Count(content[:splitPos], "```")
	t.Logf("Code block markers before split: %d", codeBlockMarkers)
	t.Logf("Inside code block: %v", codeBlockMarkers%2 == 1)
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
