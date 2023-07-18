package pkg

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeStrings(t *testing.T) {
	testCases := []struct {
		input, expected string
	}{
		{
			input:    "git@github.com:one/two",
			expected: "github.com|one/two",
		},
		{
			input:    "https://github.com/one/two",
			expected: "github.com|one/two",
		},
		{
			input:    "git@gitlab.com:djeebus/helm-test.git",
			expected: "gitlab.com|djeebus/helm-test",
		},
		{
			input:    "https://gitlab.com/djeebus/helm-test.git",
			expected: "gitlab.com|djeebus/helm-test",
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("case %s", tc.input), func(t *testing.T) {
			actual := normalizeRepoUrl(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
