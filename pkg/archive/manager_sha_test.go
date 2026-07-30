package archive

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractSHAFromArchiveURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantSHA string
		wantErr bool
	}{
		// GitHub formats
		{
			name:    "GitHub REST API zipball",
			url:     "https://api.github.com/repos/zapier/kubechecks/zipball/abc123def456",
			wantSHA: "abc123def456",
		},
		{
			name:    "GitHub Enterprise REST API zipball",
			url:     "https://github.example.com/api/v3/repos/zapier/kubechecks/zipball/abc123def456",
			wantSHA: "abc123def456",
		},
		{
			name:    "GitHub REST API tarball",
			url:     "https://api.github.com/repos/zapier/kubechecks/tarball/deadbeef",
			wantSHA: "deadbeef",
		},
		{
			name:    "GitHub REST API zipball full SHA",
			url:     "https://api.github.com/repos/owner/repo/zipball/a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			wantSHA: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		},
		{
			// Legacy /archive/{sha}.zip format kept for backward compat with any cached or
			// in-flight URLs that predate the switch to /zipball/.
			name:    "GitHub zip legacy",
			url:     "https://github.com/zapier/kubechecks/archive/abc123def456.zip",
			wantSHA: "abc123def456",
		},
		{
			name:    "GitHub Enterprise legacy archive",
			url:     "https://github.example.com/zapier/kubechecks/archive/abc123def456.zip",
			wantSHA: "abc123def456",
		},
		{
			name:    "GitHub legacy full SHA",
			url:     "https://github.com/owner/repo/archive/a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2.zip",
			wantSHA: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		},

		// GitLab formats
		{
			name:    "GitLab sha as first query param",
			url:     "https://gitlab.com/api/v4/projects/zapier%2Fkubechecks/repository/archive.zip?sha=abc123def456",
			wantSHA: "abc123def456",
		},
		{
			name:    "GitLab sha with trailing query params",
			url:     "https://gitlab.com/api/v4/projects/zapier%2Fkubechecks/repository/archive.zip?sha=abc123def456&path=some/path",
			wantSHA: "abc123def456",
		},
		{
			name:    "GitLab sha as non-first query param",
			url:     "https://gitlab.com/api/v4/projects/zapier%2Fkubechecks/repository/archive.zip?format=zip&sha=abc123def456",
			wantSHA: "abc123def456",
		},
		{
			name:    "GitLab self-hosted",
			url:     "https://gitlab.example.com/api/v4/projects/group%2Frepo/repository/archive.zip?sha=deadbeef",
			wantSHA: "deadbeef",
		},

		// Error cases
		{
			name:    "unrecognized URL format",
			url:     "https://example.com/repo/download/abc123.zip",
			wantErr: true,
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
		{
			name:    "GitHub archive URL with no filename after slash",
			url:     "https://github.com/owner/repo/archive/",
			wantErr: true,
		},
		{
			name:    "GitHub zipball URL with empty SHA",
			url:     "https://api.github.com/repos/owner/repo/zipball/",
			wantErr: true,
		},
		{
			name:    "GitLab URL with empty sha param",
			url:     "https://gitlab.com/api/v4/projects/group%2Frepo/repository/archive.zip?sha=",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sha, err := extractSHAFromArchiveURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, sha)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantSHA, sha)
			}
		})
	}
}
