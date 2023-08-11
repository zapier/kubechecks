package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStringUsages(t *testing.T) {
	tests := map[string]struct {
		expected string
		name     string
		opt      DocOpt[any]
		usage    string
	}{
		"string with choices": {
			name: "simple-string",
			opt: DocOpt[any]{
				choices: []string{
					"blah",
					"test",
				},
			},
			usage:    "This is a test.",
			expected: "This is a test. One of blah, test. (KUBECHECKS_SIMPLE_STRING)",
		},
		"string with no choices": {
			name:     "string",
			opt:      DocOpt[any]{},
			usage:    "This is a test.",
			expected: "This is a test. (KUBECHECKS_STRING)",
		},
	}

	for testName, test := range tests {
		t.Run(testName, func(t *testing.T) {
			actual := generateUsage(test.opt, test.usage, test.name)
			assert.Equal(t, test.expected, actual)
		})
	}
}
