package msg

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestByteSplitMarkdown(t *testing.T) {
	t.Run("content fits in one piece", func(t *testing.T) {
		result := byteSplitMarkdown("hello", 100)
		require.Len(t, result, 1)
		assert.Equal(t, "hello", result[0])
	})

	t.Run("content is split across pieces", func(t *testing.T) {
		content := strings.Repeat("a", 100)
		result := byteSplitMarkdown(content, 30)

		joined := strings.Join(result, "")
		assert.Equal(t, content, joined)
		for _, part := range result[:len(result)-1] {
			assert.LessOrEqual(t, len(part), 30)
		}
	})

	t.Run("code fence is closed and reopened", func(t *testing.T) {
		content := "before\n```\ncode line 1\ncode line 2\ncode line 3\n```\nafter"
		result := byteSplitMarkdown(content, 30)

		require.Greater(t, len(result), 1)

		openFenceFound := false
		for i, part := range result {
			if i > 0 && i < len(result)-1 {
				if hasOpenCodeFence(part[:strings.LastIndex(part, "```")]) {
					continue
				}
			}
			_ = part
		}
		_ = openFenceFound

		for _, part := range result {
			fenceCount := strings.Count(part, "```")
			assert.Equal(t, 0, fenceCount%2, "chunk should have balanced code fences: %q", part)
		}
	})

	t.Run("details tags are closed and reopened", func(t *testing.T) {
		content := "<details>\n<summary>title</summary>\n" + strings.Repeat("x", 100) + "\n</details>"
		result := byteSplitMarkdown(content, 60)

		require.Greater(t, len(result), 1)
		for _, part := range result {
			opens := strings.Count(part, "<details>")
			closes := strings.Count(part, "</details>")
			assert.Equal(t, opens, closes, "chunk should have balanced details tags: %q", part)
		}
	})
}

func TestHasOpenCodeFence(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"no fences", "plain text", false},
		{"closed fence", "```\ncode\n```", false},
		{"open fence", "```\ncode", true},
		{"two closed fences", "```\na\n```\n```\nb\n```", false},
		{"one open after closed", "```\na\n```\n```\nb", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasOpenCodeFence(tt.text))
		})
	}
}

func TestCountOpen(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"none", "plain text", 0},
		{"one open", "<details>content", 1},
		{"balanced", "<details>content</details>", 0},
		{"two open one closed", "<details><details>inner</details>", 1},
		{"more closes than opens", "</details></details>", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, countOpen(tt.text, "<details>", "</details>"))
		})
	}
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
