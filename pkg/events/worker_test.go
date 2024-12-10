package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertJsonToYamlManifests(t *testing.T) {
	testcases := map[string]struct {
		input, expected []string
	}{
		"empty": {
			input:    []string{},
			expected: nil,
		},
		"easy json": {
			input: []string{
				`{"hello": "world"}`,
			},
			expected: []string{
				`---
hello: world
`,
			},
		},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := convertJsonToYamlManifests(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
