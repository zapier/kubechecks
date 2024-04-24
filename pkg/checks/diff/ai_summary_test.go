package diff

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanUpAiSummary(t *testing.T) {
	t.Run("prefix", func(t *testing.T) {
		input := "```markdown\nhello\nworld"
		expected := "hello\nworld"

		actual := cleanUpAiSummary(input)
		assert.Equal(t, expected, actual)
	})

	t.Run("suffix", func(t *testing.T) {
		input := "\nhello\nworld```"
		expected := "hello\nworld"

		actual := cleanUpAiSummary(input)
		assert.Equal(t, expected, actual)
	})

	t.Run("prefix and suffix", func(t *testing.T) {
		input := "```markdown\n\nhello\nworld```"
		expected := "hello\nworld"

		actual := cleanUpAiSummary(input)
		assert.Equal(t, expected, actual)
	})

	t.Run("weird prefix and suffix", func(t *testing.T) {
		input := "```plaintext\n#***\n- Added environment variables FF_TIMESTAMPS and FF_SCRIPT_SECTIONS\n#***"
		expected := "- Added environment variables FF_TIMESTAMPS and FF_SCRIPT_SECTIONS"

		actual := cleanUpAiSummary(input)
		assert.Equal(t, expected, actual)
	})
}
