package events

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetK8sVersionWithFallback(t *testing.T) {
	const fallbackVersion = "v1.25.0"

	testcases := map[string]struct {
		version  string
		err      error
		expected string
	}{
		"success with major.minor": {
			version:  "v1.28",
			err:      nil,
			expected: "v1.28.0",
		},
		"success with full version - patch zeroed": {
			version:  "v1.28.5",
			err:      nil,
			expected: "v1.28.0",
		},
		"success with build metadata - patch zeroed": {
			version:  "v1.28.5+k3s1",
			err:      nil,
			expected: "v1.28.0",
		},
		"error returns fallback": {
			version:  "",
			err:      errors.New("cluster not found"),
			expected: fallbackVersion,
		},
		"empty version with no error returns fallback": {
			version:  "",
			err:      errors.New("no version found"),
			expected: fallbackVersion,
		},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := getK8sVersionWithFallback(tc.version, tc.err, fallbackVersion)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestNormalizeK8sVersion(t *testing.T) {
	testcases := map[string]struct {
		input    string
		expected string
	}{
		"major only": {
			input:    "v1",
			expected: "v1.0.0",
		},
		"major.minor": {
			input:    "v1.2",
			expected: "v1.2.0",
		},
		"full version - patch zeroed": {
			input:    "v1.2.3",
			expected: "v1.2.0",
		},
		"version with build metadata - patch zeroed": {
			input:    "v1.2.3+debug1",
			expected: "v1.2.0",
		},
		"version with prerelease - patch zeroed": {
			input:    "v1.2.3-alpha",
			expected: "v1.2.0",
		},
		"version with prerelease and build - patch zeroed": {
			input:    "v1.2.3-beta+build.123",
			expected: "v1.2.0",
		},
		"major.minor without v prefix": {
			input:    "1.28",
			expected: "v1.28.0",
		},
		"full version without v prefix - patch zeroed": {
			input:    "1.28.5",
			expected: "v1.28.0",
		},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := normalizeK8sVersion(tc.input)
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
