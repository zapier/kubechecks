package msg

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg"
)

func TestSplitIntoChunks(t *testing.T) {
	cfg := ChunkConfig{
		MaxLength:  500,
		MaxChunks:  0,
		Identifier: "test",
	}

	t.Run("single section fits in one chunk", func(t *testing.T) {
		sections := []string{"short app output"}
		result := SplitIntoChunks(sections, "_footer_", cfg)

		require.Len(t, result, 1)
		assert.Contains(t, result[0], "# Kubechecks test Report\n")
		assert.Contains(t, result[0], "short app output")
		assert.Contains(t, result[0], "_footer_")
		assert.NotContains(t, result[0], "Part")
	})

	t.Run("multiple sections fit in one chunk", func(t *testing.T) {
		sections := []string{"app-1 output", "app-2 output"}
		result := SplitIntoChunks(sections, "_footer_", cfg)

		require.Len(t, result, 1)
		assert.Contains(t, result[0], "app-1 output")
		assert.Contains(t, result[0], "app-2 output")
		assert.Contains(t, result[0], "_footer_")
	})

	t.Run("multiple sections require two chunks", func(t *testing.T) {
		section := strings.Repeat("x", 200)
		sections := []string{section, section, section}
		result := SplitIntoChunks(sections, "_f_", cfg)

		require.Greater(t, len(result), 1)
		assert.Contains(t, result[0], "Part 1 of")
		assert.Contains(t, result[0], "Continued in next comment.")
		assert.NotContains(t, result[0], "_f_")

		last := result[len(result)-1]
		assert.Contains(t, last, fmt.Sprintf("Part %d of %d", len(result), len(result)))
		assert.Contains(t, last, "Continued from previous comment.")
		assert.Contains(t, last, "_f_")
	})

	t.Run("empty sections produce no-changes chunk", func(t *testing.T) {
		result := SplitIntoChunks(nil, "_footer_", cfg)

		require.Len(t, result, 1)
		assert.Contains(t, result[0], "No changes")
		assert.Contains(t, result[0], "_footer_")
	})

	t.Run("max chunks cap is honoured", func(t *testing.T) {
		section := strings.Repeat("x", 200)
		sections := []string{section, section, section, section, section}
		capped := ChunkConfig{MaxLength: 500, MaxChunks: 2, Identifier: "test"}
		result := SplitIntoChunks(sections, "_f_", capped)

		require.Len(t, result, 2)
		assert.Contains(t, result[1], "Report exceeded the maximum number of comments")
	})
}

func TestChunkHeader(t *testing.T) {
	t.Run("single chunk has no part number", func(t *testing.T) {
		h := chunkHeader("staging", 1, 1)
		assert.Equal(t, "# Kubechecks staging Report\n", h)
	})

	t.Run("multi chunk has part number", func(t *testing.T) {
		h := chunkHeader("staging", 2, 5)
		assert.Equal(t, "# Kubechecks staging Report (Part 2 of 5)\n", h)
	})
}

func TestSplitDetailsAtLines(t *testing.T) {
	t.Run("content fits in one piece", func(t *testing.T) {
		result := splitDetailsAtLines("short content", 100)
		require.Len(t, result, 1)
		assert.Equal(t, "short content", result[0])
	})

	t.Run("splits at line boundaries", func(t *testing.T) {
		lines := "line1\nline2\nline3\nline4\nline5\n"
		result := splitDetailsAtLines(lines, 18)

		require.Greater(t, len(result), 1)
		joined := strings.Join(result, "")
		assert.Equal(t, lines, joined)
		for _, part := range result {
			assert.True(t, strings.HasSuffix(part, "\n"), "each piece should end at a line boundary: %q", part)
		}
	})

	t.Run("code fence is closed and reopened with language", func(t *testing.T) {
		content := "```diff\n-old\n+new\n+more\n+lines\n+here\n```\n"
		result := splitDetailsAtLines(content, 30)

		require.Greater(t, len(result), 1)
		for _, part := range result {
			opens := strings.Count(part, "```")
			assert.Equal(t, 0, opens%2, "each piece should have balanced fences: %q", part)
		}
		assert.True(t, strings.HasPrefix(result[0], "```diff\n"))
		for _, part := range result[1:] {
			assert.True(t, strings.HasPrefix(part, "```diff\n"),
				"continuation should reopen with language hint: %q", part)
		}
	})

	t.Run("plain code fence without language", func(t *testing.T) {
		content := "```\ncode1\ncode2\ncode3\ncode4\n```\n"
		result := splitDetailsAtLines(content, 20)

		require.Greater(t, len(result), 1)
		for _, part := range result[1:] {
			assert.True(t, strings.HasPrefix(part, "```\n"),
				"continuation should reopen fence: %q", part)
		}
	})

	t.Run("content without code fence", func(t *testing.T) {
		content := "text line 1\ntext line 2\ntext line 3\ntext line 4\n"
		result := splitDetailsAtLines(content, 25)

		require.Greater(t, len(result), 1)
		joined := strings.Join(result, "")
		assert.Equal(t, content, joined)
	})

	t.Run("oversized single line is truncated", func(t *testing.T) {
		longLine := strings.Repeat("x", 200) + "\n"
		content := "short\n" + longLine + "also short\n"
		result := splitDetailsAtLines(content, 80)

		for _, part := range result {
			assert.LessOrEqual(t, len(part), 80, "each piece must fit within maxLen: len=%d", len(part))
		}
		combined := strings.Join(result, "")
		assert.Contains(t, combined, "short\n")
		assert.Contains(t, combined, "... (line truncated)")
		assert.Contains(t, combined, "also short\n")
	})

	t.Run("oversized line inside code fence accounts for reopen prefix", func(t *testing.T) {
		longLine := strings.Repeat("y", 200) + "\n"
		content := "```diff\n" + longLine + "```\n"
		result := splitDetailsAtLines(content, 80)

		for i, part := range result {
			assert.LessOrEqual(t, len(part), 80,
				"piece %d must fit within maxLen: len=%d, content=%q", i, len(part), part)
		}
		assert.Contains(t, strings.Join(result, ""), "... (line truncated)")
	})
}

func TestBuildAppSections(t *testing.T) {
	mockVcs := &mockEmoji{}

	t.Run("small app fits in one section", func(t *testing.T) {
		m := NewMessage("test/repo", 1, 1, mockVcs)
		results := &AppResults{}
		results.AddCheckResult(Result{
			State:   pkg.StateSuccess,
			Summary: "Check",
			Details: "small details",
		})

		sections := m.buildAppSections("my-app", results, 10000)
		require.Len(t, sections, 1)
		assert.Contains(t, sections[0], "my-app")
		assert.Contains(t, sections[0], "small details")
	})

	t.Run("multiple checks split per-check", func(t *testing.T) {
		m := NewMessage("test/repo", 1, 1, mockVcs)
		results := &AppResults{}
		results.AddCheckResult(Result{
			State:   pkg.StateSuccess,
			Summary: "Check A",
			Details: strings.Repeat("a", 100),
		})
		results.AddCheckResult(Result{
			State:   pkg.StateSuccess,
			Summary: "Check B",
			Details: strings.Repeat("b", 100),
		})

		sections := m.buildAppSections("my-app", results, 300)

		require.Greater(t, len(sections), 1, "checks should be in separate sections")
		assert.Contains(t, sections[0], "Check A")
		assert.Contains(t, sections[1], "Check B")
		for _, s := range sections {
			assert.Contains(t, s, "my-app", "every section should have the app header")
			assert.LessOrEqual(t, len(s), 300, "each section should fit within the limit")
		}
	})

	t.Run("large check is split at line boundaries", func(t *testing.T) {
		m := NewMessage("test/repo", 1, 1, mockVcs)
		results := &AppResults{}

		var lines []string
		for i := range 50 {
			lines = append(lines, fmt.Sprintf("+line %d of the diff content", i))
		}
		bigDiff := "```diff\n" + strings.Join(lines, "\n") + "\n```"

		results.AddCheckResult(Result{
			State:   pkg.StateSuccess,
			Summary: "Diff",
			Details: bigDiff,
		})

		sections := m.buildAppSections("my-app", results, 500)

		require.Greater(t, len(sections), 1, "should split into multiple sections")
		for _, s := range sections {
			assert.Contains(t, s, "my-app", "every section should have the app header")
			assert.Contains(t, s, "<details>", "every section should be wrapped in details")
			assert.Contains(t, s, "</details>", "every section should have closing details")
			assert.LessOrEqual(t, len(s), 500, "each section should fit within the limit")
		}
		assert.Contains(t, sections[0], "Part 1 of")
		assert.Contains(t, sections[1], "Part 2 of")
	})
}

type mockEmoji struct{}

func (m *mockEmoji) ToEmoji(_ pkg.CommitState) string { return ":white_check_mark:" }
