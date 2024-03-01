package github_client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRepo(t *testing.T) {
	testcases := []struct {
		name, input                 string
		expectedOwner, expectedRepo string
	}{
		{
			name:          "github.com over ssh",
			input:         "git@github.com:zapier/kubechecks.git",
			expectedOwner: "zapier",
			expectedRepo:  "kubechecks",
		},
		{
			name:          "github.com over https",
			input:         "https://github.com/zapier/kubechecks.git",
			expectedOwner: "zapier",
			expectedRepo:  "kubechecks",
		},
		{
			name:          "github.com with https with username without .git",
			input:         "https://djeebus@github.com/zapier/kubechecks",
			expectedOwner: "zapier",
			expectedRepo:  "kubechecks",
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			owner, repo := parseRepo(tc.input)
			assert.Equal(t, tc.expectedOwner, owner)
			assert.Equal(t, tc.expectedRepo, repo)
		})
	}
}
