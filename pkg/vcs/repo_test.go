package vcs

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/config"
)

func TestGetCloneUrl(t *testing.T) {
	// common defaults
	const (
		testToken = "test-token"
		testUser  = "test-user"
	)

	testcases := []struct {
		name     string
		expected string

		vcsType    string
		vcsBaseUrl string
	}{
		{
			name:     "gitlab default",
			vcsType:  "gitlab",
			expected: "https://%s:%s@gitlab.com",
		},
		{
			name:     "github default",
			vcsType:  "github",
			expected: "https://%s:%s@github.com",
		},
		{
			name:       "can override the host",
			vcsType:    "github",
			vcsBaseUrl: "https://some.url.com/",
			expected:   "https://%s:%s@some.url.com",
		},
		{
			name:       "can override the protocol",
			vcsType:    "github",
			vcsBaseUrl: "http://some.url.com/",
			expected:   "http://%s:%s@some.url.com",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotEqual(t, "", tc.vcsType)

			cfg := config.ServerConfig{
				VcsToken:   testToken,
				VcsType:    tc.vcsType,
				VcsBaseUrl: tc.vcsBaseUrl,
			}

			actual, err := getCloneUrl(testUser, cfg)
			require.NoError(t, err)

			expected := fmt.Sprintf(tc.expected, testUser, testToken)
			require.Equal(t, expected, actual)
		})
	}
}

func TestCensorVcsToken(t *testing.T) {
	cfg := config.ServerConfig{VcsToken: "hre"}
	result := censorVcsToken(cfg, []string{"one", "two", "three"})
	assert.Equal(t, []string{"one", "two", "t********e"}, result)
}

func TestCensorEmptyVcsToken(t *testing.T) {
	cfg := config.ServerConfig{VcsToken: ""}
	result := censorVcsToken(cfg, []string{"one", "two", "three"})
	assert.Equal(t, []string{"one", "two", "three"}, result)
}
