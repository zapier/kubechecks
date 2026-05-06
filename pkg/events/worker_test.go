package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeK8sVersion(t *testing.T) {
	fallbackVersion := "fallback"

	testcases := map[string]struct {
		input    string
		expected string
	}{
		"major only": {
			input:    "v1",
			expected: "1.0.0",
		},
		"major.minor": {
			input:    "v1.2",
			expected: "1.2.0",
		},
		"full version - patch zeroed": {
			input:    "v1.2.3",
			expected: "1.2.0",
		},
		"version with build metadata - patch zeroed": {
			input:    "v1.2.3+debug1",
			expected: "1.2.0",
		},
		"version with prerelease - patch zeroed": {
			input:    "v1.2.3-alpha",
			expected: "1.2.0",
		},
		"version with prerelease and build - patch zeroed": {
			input:    "v1.2.3-beta+build.123",
			expected: "1.2.0",
		},
		"major.minor without v prefix": {
			input:    "1.28",
			expected: "1.28.0",
		},
		"full version without v prefix - patch zeroed": {
			input:    "1.28.5",
			expected: "1.28.0",
		},
		"invalid version": {
			input:    "just-testing",
			expected: fallbackVersion,
		},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := normalizeK8sVersion(tc.input, fallbackVersion)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

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
