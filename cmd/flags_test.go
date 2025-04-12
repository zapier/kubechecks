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
		"string with out of order choices": {
			name: "simple-string",
			opt: DocOpt[any]{
				choices: []string{
					"test",
					"blah",
				},
			},
			usage:    "This is a test.",
			expected: "This is a test. One of test, blah. (KUBECHECKS_SIMPLE_STRING)",
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

func TestCombine(t *testing.T) {
	tests := map[string]struct {
		dst      DocOpt[any]
		src      DocOpt[any]
		expected DocOpt[any]
	}{
		"combine choices": {
			dst: DocOpt[any]{},
			src: DocOpt[any]{
				choices: []string{"choice1", "choice2"},
			},
			expected: DocOpt[any]{
				choices: []string{"choice1", "choice2"},
			},
		},
		"combine default value": {
			dst: DocOpt[any]{},
			src: DocOpt[any]{
				defaultValue: ptr[any]("default"),
			},
			expected: DocOpt[any]{
				defaultValue: ptr[any]("default"),
			},
		},
		"combine shorthand": {
			dst: DocOpt[any]{},
			src: DocOpt[any]{
				shorthand: ptr("s"),
			},
			expected: DocOpt[any]{
				shorthand: ptr("s"),
			},
		},
		"combine all fields": {
			dst: DocOpt[any]{},
			src: DocOpt[any]{
				choices:      []string{"choice1", "choice2"},
				defaultValue: ptr[any]("default"),
				shorthand:    ptr("s"),
			},
			expected: DocOpt[any]{
				choices:      []string{"choice1", "choice2"},
				defaultValue: ptr[any]("default"),
				shorthand:    ptr("s"),
			},
		},
		"preserve existing dst values when src is empty": {
			dst: DocOpt[any]{
				choices:      []string{"existing"},
				defaultValue: ptr[any]("existing"),
				shorthand:    ptr("e"),
			},
			src: DocOpt[any]{},
			expected: DocOpt[any]{
				choices:      []string{"existing"},
				defaultValue: ptr[any]("existing"),
				shorthand:    ptr("e"),
			},
		},
	}

	for testName, test := range tests {
		t.Run(testName, func(t *testing.T) {
			combine(&test.dst, test.src)
			assert.Equal(t, test.expected, test.dst)
		})
	}
}

// Helper function to create pointers for test values
func ptr[T any](v T) *T {
	return &v
}
