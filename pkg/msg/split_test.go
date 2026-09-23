package msg

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg"
)

var fenceLine = regexp.MustCompile("(?m)^ {0,3}```")

// assertBalanced counts fences and tags with its own rules, not with fenceOf and mdState
func assertBalanced(t *testing.T, markdown string, msgAndArgs ...any) {
	t.Helper()

	assert.Zero(t, len(fenceLine.FindAllString(markdown, -1))%2, msgAndArgs...)
	assert.Equal(t, strings.Count(markdown, "<details>"), strings.Count(markdown, "</details>"), msgAndArgs...)
}

func TestFenceOf(t *testing.T) {
	for _, tc := range []struct{ line, marker, info string }{
		{"```diff\n", "```", "diff"},
		{" ```\n", "```", ""},
		{"````\n", "````", ""},
		{"~~~\n", "", ""},
		{"    ```\n", "", ""},
		{"+```\n", "", ""},
		{"``\n", "", ""},
		{"plain text\n", "", ""},
	} {
		marker, info := fenceOf(tc.line)
		assert.Equal(t, tc.marker, marker, "%q", tc.line)
		assert.Equal(t, tc.info, info, "%q", tc.line)
	}
}

func TestMdState_CloseThenOpenOnOneLine(t *testing.T) {
	state := mdState{}.next("<details>\n").next("<summary>outer</summary>\n")
	state = state.next("</details><details>\n")

	require.Len(t, state.details, 1)
	assert.False(t, state.details[0].hasSummary, "the outer block is closed, this one has no summary yet")
}

func TestSplitDetailsAtLines(t *testing.T) {
	t.Run("what fits is left alone", func(t *testing.T) {
		assert.Equal(t, []string{"short content"}, splitDetailsAtLines("short content", 100))
	})

	t.Run("a fence is closed at the cut and opened again", func(t *testing.T) {
		parts := splitDetailsAtLines("```diff\n+one\n+two\n+three\n```\n", 24)

		assert.Equal(t, []string{
			"```diff\n+one\n+two\n```\n",
			"```diff\n+three\n```\n",
		}, parts)
	})

	t.Run("plain text is only cut between lines", func(t *testing.T) {
		lines := "line1\nline2\nline3\nline4\nline5\n"
		parts := splitDetailsAtLines(lines, 18)

		require.Greater(t, len(parts), 1)
		assert.Equal(t, lines, strings.Join(parts, ""))
		for _, part := range parts {
			assert.True(t, strings.HasSuffix(part, "\n"), "%q", part)
		}
	})
}

func TestSplitDetailsAtLines_LongLineKeepsMarker(t *testing.T) {
	content := "<details>\n<summary>hooks</summary>\n\n```json\n" + strings.Repeat("x", 500) + "\n" + strings.Repeat("y", 500) + "\n```\n</details>"

	parts := splitDetailsAtLines(content, 150)

	for i, part := range parts {
		assert.LessOrEqual(t, len(part), 150, "part %d", i)
		assertBalanced(t, part, "part %d", i)
	}
	assert.Equal(t, 2, strings.Count(strings.Join(parts, ""), lineTruncated))
}

func TestSplitDetailsAtLines_KeepsValidUTF8(t *testing.T) {
	line := strings.Repeat("é", 200) + "\n"
	for maxLen := 60; maxLen < 64; maxLen++ {
		for _, part := range splitDetailsAtLines(line+line, maxLen) {
			assert.True(t, utf8.ValidString(part), "maxLen=%d", maxLen)
			assert.LessOrEqual(t, len(part), maxLen)
		}
	}
}

func TestSplitDetailsAtLines_NestedDetails(t *testing.T) {
	content := "<details>\n<summary>PreSync hooks</summary>\n\n```yaml\n" +
		strings.Repeat("key: value\n", 50) + "```\n</details>"

	parts := splitDetailsAtLines(content, 200)

	require.Greater(t, len(parts), 1)
	for i, part := range parts {
		assertBalanced(t, part, "part %d", i)
		assert.LessOrEqual(t, len(part), 200, "part %d", i)
	}
	assert.Contains(t, parts[1], "<summary>PreSync hooks (continued)</summary>")
}

// a ConfigMap holding markdown puts fence-looking lines inside the diff block
func TestSplitDetailsAtLines_FenceInsideDiff(t *testing.T) {
	content := "```diff\n" + strings.Repeat(" context\n", 10) + " ```yaml\n" +
		strings.Repeat("+added line\n", 30) + "```\n"

	parts := splitDetailsAtLines(content, 150)

	require.Greater(t, len(parts), 1)
	for i, part := range parts {
		assert.True(t, strings.HasPrefix(part, "```diff\n"), "part %d", i)
		assert.True(t, strings.HasSuffix(part, "```\n"), "part %d", i)
	}
}

// an error message is put in a fence as is, and can hold a fence line of its own
func TestSplitDetailsAtLines_UnbalancedInput(t *testing.T) {
	content := "```\n" + strings.Repeat("some error output\n", 20) + "```\n" + strings.Repeat("more output\n", 20) + "```\n"

	parts := splitDetailsAtLines(content, 200)

	require.Greater(t, len(parts), 1)
	for i, part := range parts {
		assertBalanced(t, part, "part %d", i)
	}
}

func TestBuildAppSections(t *testing.T) {
	newMessage := func() *Message { return NewMessage("test/repo", 1, 1, fakeEmojiable{":ok:"}) }

	t.Run("an app that fits is one section", func(t *testing.T) {
		results := &AppResults{}
		results.AddCheckResult(Result{State: pkg.StateSuccess, Summary: "Check", Details: "small details"})

		sections := newMessage().buildAppSections("my-app", results, 10000)

		require.Len(t, sections, 1)
		assert.Contains(t, sections[0], "my-app")
		assert.Contains(t, sections[0], "small details")
	})

	t.Run("checks that fit together stay together", func(t *testing.T) {
		results := &AppResults{}
		for _, name := range []string{"A", "B", "C"} {
			results.AddCheckResult(Result{State: pkg.StateSuccess, Summary: name, Details: strings.Repeat(name, 100)})
		}
		m := newMessage()
		whole := len(m.buildAppSections("app", results, 10000)[0])

		// room for two checks, not three
		sections := m.buildAppSections("app", results, whole-50)

		require.Len(t, sections, 2)
		assert.Contains(t, sections[0], checkSeparator)
		assert.NotContains(t, sections[1], checkSeparator)
	})

	t.Run("a check that is too big is cut into numbered parts", func(t *testing.T) {
		var lines []string
		for i := range 50 {
			lines = append(lines, fmt.Sprintf("+line %d of the diff content", i))
		}
		results := &AppResults{}
		results.AddCheckResult(Result{State: pkg.StateSuccess, Summary: "Diff", Details: "```diff\n" + strings.Join(lines, "\n") + "\n```"})

		sections := newMessage().buildAppSections("my-app", results, 500)

		require.Greater(t, len(sections), 1)
		for i, section := range sections {
			assert.Contains(t, section, "my-app", "section %d", i)
			assert.Contains(t, section, fmt.Sprintf("(Part %d of %d)", i+1, len(sections)))
			assert.LessOrEqual(t, len(section), 500, "section %d", i)
			assertBalanced(t, section, "section %d", i)
		}
	})
}

func TestPackSections(t *testing.T) {
	big := strings.Repeat("x", 50)

	assert.Nil(t, packSections(nil, 10))
	assert.Equal(t, [][]string{{"aaaa", "bbbb"}, {"cccc"}}, packSections([]string{"aaaa", "bbbb", "cccc"}, 8))
	assert.Equal(t, [][]string{{"aaaa"}, {big}, {"bbbb"}}, packSections([]string{"aaaa", big, "bbbb"}, 8))
}

func TestSplitIntoChunks(t *testing.T) {
	cfg := chunkConfig{MaxLength: 500, MaxChunks: pkg.MaxCommentsPerCheck, Identifier: "test", Footer: "_footer_"}

	t.Run("sections that fit share one chunk", func(t *testing.T) {
		result := splitIntoChunks([]string{"app-1 output", "app-2 output"}, cfg)

		require.Len(t, result, 1)
		assert.Equal(t, "# Kubechecks test Report\napp-1 outputapp-2 output\n\n_footer_", result[0])
	})

	t.Run("several chunks are numbered and linked", func(t *testing.T) {
		section := strings.Repeat("x", 200)
		result := splitIntoChunks([]string{section, section, section}, cfg)

		require.Greater(t, len(result), 1)
		assert.Contains(t, result[0], "Part 1 of")
		assert.Contains(t, result[0], "Continued in next comment.")
		assert.NotContains(t, result[0], "_footer_")

		last := result[len(result)-1]
		assert.Contains(t, last, fmt.Sprintf("Part %d of %d", len(result), len(result)))
		assert.Contains(t, last, "Continued from previous comment.")
		assert.Contains(t, last, "_footer_")
	})

	t.Run("nothing to report", func(t *testing.T) {
		result := splitIntoChunks(nil, cfg)

		require.Len(t, result, 1)
		assert.Contains(t, result[0], "No changes")
		assert.Contains(t, result[0], "_footer_")
	})
}

// the last chunk carries the footer and, when capped, the truncation note; both count against MaxLength
func TestSplitIntoChunks_Cap(t *testing.T) {
	for _, maxChunks := range []int{1, 2} {
		cfg := chunkConfig{MaxLength: 2000, MaxChunks: maxChunks, Identifier: "id", Footer: "footer"}
		section := strings.Repeat("x", cfg.sectionBudget())

		chunks := splitIntoChunks([]string{section, section, section}, cfg)

		require.Len(t, chunks, maxChunks)
		assert.True(t, strings.HasSuffix(chunks[maxChunks-1], "footer"+truncatedNote))
		for i, chunk := range chunks {
			assert.LessOrEqual(t, len(chunk), cfg.MaxLength, "chunk %d", i)
		}
	}

	single := splitIntoChunks([]string{"a"}, chunkConfig{MaxLength: 2000, MaxChunks: 1, Identifier: "id"})
	assert.True(t, strings.HasPrefix(single[0], "# Kubechecks id Report\n"), "one comment is not a part of anything")
}

// a check summary is never split, so a huge one yields a section over budget; the chunk is cut to fit
func TestSplitIntoChunks_CutsWhatCannotFit(t *testing.T) {
	cfg := chunkConfig{MaxLength: 500, MaxChunks: pkg.MaxCommentsPerCheck, Identifier: "id", Footer: "footer"}
	huge := "<details>\n<summary>\n\n## app\n</summary>\n\n```diff\n" + strings.Repeat("+é\n", 1000) + "```\n</details>"

	chunks := splitIntoChunks([]string{"small", huge, "small"}, cfg)

	require.Len(t, chunks, 3)
	for i, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), cfg.MaxLength, "chunk %d", i)
		assert.True(t, utf8.ValidString(chunk), "chunk %d", i)
		assertBalanced(t, chunk, "chunk %d", i)
	}
	assert.True(t, strings.HasSuffix(chunks[1], cutShortNote+continuedIn), "the next comment is still announced")
	assert.NotContains(t, chunks[1], truncatedNote)
}

// 40 apps with a diff each do not fit in one GitHub comment
func TestBuildComment_LargeReportKeepsEveryApp(t *testing.T) {
	const appCount = 40

	m := NewMessage("test/repo", 1, 1, fakeEmojiable{":ok:"})
	m.apps = make(map[string]*AppResults, appCount)
	for i := range appCount {
		name := fmt.Sprintf("app-%02d", i)

		var diff strings.Builder
		diff.WriteString("```diff\n")
		for line := range 60 {
			fmt.Fprintf(&diff, "+  %s-key-%03d: registry.example.com/%s/component:v1.2.%d\n", name, line, name, line)
		}
		diff.WriteString("```\n")

		results := &AppResults{}
		results.AddCheckResult(Result{State: pkg.StateWarning, Summary: "Diff", Details: diff.String()})
		m.apps[name] = results
	}

	// MaxComments left at zero means no cap of the caller's own
	chunks := m.BuildComment(context.Background(), CommentOptions{
		Start: time.Now(), CommitSHA: "commit-sha", Identifier: "test",
		AppsChecked: appCount, TotalChecked: appCount, MaxLength: 64 * 1024,
	})

	require.Greater(t, len(chunks), 1)
	joined := strings.Join(chunks, "")
	for i := range appCount {
		assert.Equal(t, 1, strings.Count(joined, fmt.Sprintf("`app-%02d`", i)), "app-%02d", i)
	}
	assert.NotContains(t, joined, truncatedNote)
}

func TestBuildComment_EveryChunkFitsTheLimit(t *testing.T) {
	diff := "```diff\n" + strings.Repeat("+  some: évalue that changed\n", 3000) + "```\n"
	hooks := "<details>\n<summary>PreSync</summary>\n\n```yaml\n" + strings.Repeat("key: value\n", 2000) + "```\n</details>"

	m := NewMessage("test/repo", 1, 1, fakeEmojiable{":ok:"})
	for _, app := range []string{"app-a", "app-b", "app-c"} {
		m.AddNewApp(context.TODO(), app)
		m.AddToAppMessage(context.TODO(), app, Result{State: pkg.StateWarning, Summary: "diff", Details: diff})
		m.AddToAppMessage(context.TODO(), app, Result{State: pkg.StateSuccess, Summary: "hooks", Details: hooks})
		m.AddToAppMessage(context.TODO(), app, Result{State: pkg.StateSuccess, Summary: "small", Details: "ok"})
	}

	for _, limit := range []int{600, 1000, 4096, 64 * 1024, 1_000_000} {
		chunks := m.BuildComment(context.TODO(), CommentOptions{
			Start: time.Now(), CommitSHA: "sha", Identifier: "id", AppsChecked: 3, TotalChecked: 3,
			MaxLength: limit, MaxComments: pkg.MaxCommentsPerCheck,
		})

		for i, chunk := range chunks {
			require.LessOrEqual(t, len(chunk), limit, "limit %d, chunk %d of %d", limit, i+1, len(chunks))
			require.True(t, utf8.ValidString(chunk), "limit %d, chunk %d", limit, i+1)
			assertBalanced(t, chunk, "limit %d, chunk %d", limit, i+1)
		}
	}
}

// a report that takes one comment is sized with that comment's overhead, not with the room the parts of a split report leave
func TestBuildComment_ExactlyAtTheLimit(t *testing.T) {
	m := NewMessage("test/repo", 1, 1, fakeEmojiable{":ok:"})
	m.AddNewApp(context.TODO(), "app-a")
	m.AddToAppMessage(context.TODO(), "app-a", Result{State: pkg.StateWarning, Summary: "diff", Details: "```diff\n" + strings.Repeat("+line\n", 200) + "```\n"})
	m.AddNewApp(context.TODO(), "app-b")
	m.AddToAppMessage(context.TODO(), "app-b", Result{State: pkg.StateSuccess, Summary: "diff", Details: "ok"})

	opts := CommentOptions{
		Start: time.Now(), CommitSHA: "sha", Identifier: "id", AppsChecked: 2, TotalChecked: 2,
		MaxLength: 64 * 1024, MaxComments: pkg.MaxCommentsPerCheck,
	}

	whole := m.BuildComment(context.TODO(), opts)
	require.Len(t, whole, 1)

	opts.MaxLength = len(whole[0])
	assert.Equal(t, whole, m.BuildComment(context.TODO(), opts), "fits exactly, still one comment")

	opts.MaxLength--
	assert.Greater(t, len(m.BuildComment(context.TODO(), opts)), 1, "one byte over, split")
}

// a section bigger than the budget is packed alone, the stop must count chunks and not bytes
func TestBuildComment_OversizedSectionKeepsTheCap(t *testing.T) {
	m := NewMessage("test/repo", 1, 1, fakeEmojiable{":ok:"})
	m.AddNewApp(context.TODO(), "app-a")
	m.AddToAppMessage(context.TODO(), "app-a", Result{State: pkg.StateWarning, Summary: strings.Repeat("s", 2000), Details: "a diff"})
	for _, app := range []string{"app-b", "app-c"} {
		m.AddNewApp(context.TODO(), app)
		m.AddToAppMessage(context.TODO(), app, Result{State: pkg.StateSuccess, Summary: "diff", Details: strings.Repeat("y", 1000)})
	}

	comments := m.BuildComment(context.TODO(), CommentOptions{
		Start: time.Now(), CommitSHA: "sha", Identifier: "id", AppsChecked: 3, TotalChecked: 3,
		MaxLength: 1500, MaxComments: 2,
	})

	require.Len(t, comments, 2)
	assert.Contains(t, comments[1], "`app-b`")
	assert.True(t, strings.HasSuffix(comments[1], truncatedNote))
}

// the cap does not change what fits a comment: what is left out is still noted
func TestBuildComment_CapOfOneKeepsTheNote(t *testing.T) {
	m := NewMessage("test/repo", 1, 1, fakeEmojiable{":ok:"})
	for _, app := range []string{"app-a", "app-b", "app-c"} {
		m.AddNewApp(context.TODO(), app)
		m.AddToAppMessage(context.TODO(), app, Result{State: pkg.StateSuccess, Summary: "diff", Details: strings.Repeat("d", 800)})
	}

	comments := m.BuildComment(context.TODO(), CommentOptions{
		Start: time.Now(), CommitSHA: "sha", Identifier: "id", AppsChecked: 3, TotalChecked: 3,
		MaxLength: 1952, MaxComments: 1,
	})

	require.Len(t, comments, 1)
	assert.LessOrEqual(t, len(comments[0]), 1952)
	assert.Contains(t, comments[0], "`app-a`")
	assert.NotContains(t, comments[0], "`app-c`")
	assert.True(t, strings.HasSuffix(comments[0], truncatedNote), "the apps left out are noted")
}

func TestBuildSections_StopsPastTheCap(t *testing.T) {
	m := NewMessage("test/repo", 1, 1, fakeEmojiable{":ok:"})
	var names []string
	for i := range 50 {
		name := fmt.Sprintf("app-%02d", i)
		names = append(names, name)
		m.AddNewApp(context.TODO(), name)
		m.AddToAppMessage(context.TODO(), name, Result{State: pkg.StateSuccess, Summary: "diff", Details: strings.Repeat("x", 100)})
	}

	// every app is one section of the same size, so ten chunks is ten apps
	budget := len(m.buildSections(names[:1], 10000, 1)[0])

	sections := m.buildSections(names, budget, 10)

	assert.Len(t, sections, 11, "one section past the cap, then it stops")
}
