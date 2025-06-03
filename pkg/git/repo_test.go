package git

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/config"
)

func wipe(t *testing.T, path string) {
	err := os.RemoveAll(path)
	require.NoError(t, err)
}

func TestRepoRoundTrip(t *testing.T) {
	originRepo, err := os.MkdirTemp("", "kubechecks-test-")
	require.NoError(t, err)
	defer wipe(t, originRepo)

	// initialize the test repo
	cmd := exec.Command("/bin/sh", "-c", `#!/usr/bin/env bash
set -e
set -x

# set up git repo
cd $TEMPDIR
git init
git config user.email "user@test.com"
git config user.name "Zap Zap"

# set up main branch
git branch -m main

echo "one" > abc.txt
git add abc.txt
git commit -m "commit one on main"

# set up testing branch
git checkout -b testing
echo "three" > abc.txt
git add abc.txt
git commit -m "commit two on testing"

# add commit back to main
git checkout main
echo "four" > def.txt
git add def.txt
git commit -m "commit two on main"

# pull main into testing
git checkout testing
git merge main
echo "two" > ghi.txt
git add ghi.txt
git commit -m "commit three"
`)
	cmd.Env = append(cmd.Env, "TEMPDIR="+originRepo)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	err = cmd.Run()
	require.NoError(t, err)

	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = originRepo
	output, err := cmd.Output()
	require.NoError(t, err)
	sha := strings.TrimSpace(string(output))

	var cfg config.ServerConfig
	ctx := context.Background()
	repo := New(cfg, originRepo, "main")

	err = repo.Clone(ctx)
	require.NoError(t, err)
	defer wipe(t, repo.Directory)

	err = repo.MergeIntoTarget(ctx, sha)
	require.NoError(t, err)

	files, err := repo.GetListOfChangedFiles(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"abc.txt", "ghi.txt"}, files)
}

func TestRepoGetRemoteHead(t *testing.T) {
	cfg := config.ServerConfig{}
	ctx := context.TODO()

	repo := New(cfg, "https://github.com/zapier/kubechecks.git", "")
	repo.Shallow = true
	repo.BranchName = "gh-pages"
	err := repo.Clone(ctx)
	require.NoError(t, err)

	t.Cleanup(repo.Wipe)

	branch, err := repo.GetRemoteHead()
	require.NoError(t, err)
	assert.Equal(t, "main", branch)
	currentBranch, err := repo.GetCurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "gh-pages", currentBranch)
}

func TestGetCloneUrl(t *testing.T) {
	tests := []struct {
		name           string
		user           string
		cfg            config.ServerConfig
		httpClient     HTTPClient
		expectedResult string
		expectError    bool
	}{
		{
			name: "basic case with default hostname - github",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl: "",
				VcsType:    "github",
				VcsToken:   "token123",
			},
			httpClient:     nil, // not needed for this case
			expectedResult: "https://testuser:token123@github.com",
			expectError:    false,
		},
		{
			name: "basic case with default hostname - gitlab",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl: "",
				VcsType:    "gitlab",
				VcsToken:   "glpat-token123",
			},
			httpClient:     nil,
			expectedResult: "https://testuser:glpat-token123@gitlab.com",
			expectError:    false,
		},
		{
			name: "custom VcsBaseUrl",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl: "https://git.example.com",
				VcsType:    "github",
				VcsToken:   "token123",
			},
			httpClient:     nil,
			expectedResult: "https://testuser:token123@git.example.com",
			expectError:    false,
		},
		{
			name: "custom VcsBaseUrl with http scheme",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl: "http://git.internal.com",
				VcsType:    "github",
				VcsToken:   "token123",
			},
			httpClient:     nil,
			expectedResult: "http://testuser:token123@git.internal.com",
			expectError:    false,
		},
		{
			name: "invalid VcsBaseUrl",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl: "://invalid-url",
				VcsType:    "github",
				VcsToken:   "token123",
			},
			httpClient:  nil,
			expectError: true,
		},
		{
			name: "GitHub App authentication - success",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl:           "",
				VcsType:              "github",
				VcsToken:             "token123",
				GithubAppID:          123,
				GithubInstallationID: 456,
				GithubPrivateKey:     generateTestRSAPrivateKey(t),
			},
			httpClient: &mockHTTPClient{
				doFunc: func(req *http.Request) (*http.Response, error) {
					// Verify the request
					assert.Equal(t, "POST", req.Method)
					assert.Equal(t, "https://api.github.com/app/installations/456/access_tokens", req.URL.String())
					assert.Equal(t, "application/vnd.github.v3+json", req.Header.Get("Accept"))
					assert.Contains(t, req.Header.Get("Authorization"), "Bearer ")

					// Return a mock response
					body := `{"token": "ghs_access_token_123"}`
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				},
			},
			expectedResult: "https://x-access-token:ghs_access_token_123:token123@github.com",
			expectError:    false,
		},
		{
			name: "GitHub App authentication - HTTP error",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl:           "",
				VcsType:              "github",
				VcsToken:             "token123",
				GithubAppID:          123,
				GithubInstallationID: 456,
				GithubPrivateKey:     generateTestRSAPrivateKey(t),
			},
			httpClient: &mockHTTPClient{
				doFunc: func(req *http.Request) (*http.Response, error) {
					return nil, errors.New("network error")
				},
			},
			expectError: true,
		},
		{
			name: "GitHub App authentication - invalid response body",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl:           "",
				VcsType:              "github",
				VcsToken:             "token123",
				GithubAppID:          123,
				GithubInstallationID: 456,
				GithubPrivateKey:     generateTestRSAPrivateKey(t),
			},
			httpClient: &mockHTTPClient{
				doFunc: func(req *http.Request) (*http.Response, error) {
					body := `{"invalid": json`
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				},
			},
			expectError: true,
		},
		{
			name: "GitHub App authentication - invalid private key",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl:           "",
				VcsType:              "github",
				VcsToken:             "token123",
				GithubAppID:          123,
				GithubInstallationID: 456,
				GithubPrivateKey:     "invalid-private-key",
			},
			httpClient:  nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getCloneUrl(tt.user, tt.cfg, tt.httpClient)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

// mockHTTPClient is a mock implementation of HTTPClient for testing
type mockHTTPClient struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

// generateTestRSAPrivateKey generates a test RSA private key for testing
func generateTestRSAPrivateKey(t *testing.T) string {
	// Generate a test RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Encode the private key in PEM format
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	return string(privateKeyPEM)
}
