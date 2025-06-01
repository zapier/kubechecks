package git

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/config"
)

// MockHTTPClient is a mock implementation of HTTPClient
type MockHTTPClient struct {
	mock.Mock
}

func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	return args.Get(0).(*http.Response), args.Error(1)
}

const testPrivateKey = `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEAwn1GqKisDr8cHZdM4+9UvKmJBQJN2++p4O7z4z8UHyMJHEAZ
YdYHJj+BcFY2xwjF7KnFnpkqJ8MeK4h7K5VjsLhJGhzjSsTxA8R9qE3+kGcXxXzk
8xVDGJ9o8R8cHZdM4+9UvKmJBQJN2++p4O7z4z8UHyMJHEAZYdYHJj+BcFY2xwjF
7KnFnpkqJ8MeK4h7K5VjsLhJGhzjSsTxA8R9qE3+kGcXxXzk8xVDGJ9o8R8cHZdM
4+9UvKmJBQJN2++p4O7z4z8UHyMJHEAZYdYHJj+BcFY2xwjF7KnFnpkqJ8MeK4h7
K5VjsLhJGhzjSsTxA8R9qE3+kGcXxXzk8xVDGJ9o8R8cHZdM4+9UvKmJBQJN2++p
4O7z4z8UHyMJHEAZYdYHJj+BcFY2xwjF7KnFnpkqJ8MeK4h7K5VjsLhJGhzjSsTx
A8R9qE3+kGcXxXzk8xVDGJ9o8R8cHZdM4+9UvKmJBQJN2++p4O7z4z8UHyMJHEAZ
YdYHJj+BcFY2xwjF7KnFnpkqJ8MeK4h7K5VjsLhJGhzjSsTxA8R9qE3+kGcXxXzk
8xVDGJ9o8R8cHZdM4+9UvKmJBQJN2++p4O7z4z8UHyMJHEAZYdYHJj+BcFY2xwjF
7KnFnpkqJ8MeK4h7K5VjsLhJGhzjSsTxA8R9qE3+kGcXxXzk8xVDGJ9o8R8cHZdM
4+9UvKmJBQJN2++p4O7z4z8UHyMJHEAZYdYHJj+BcFY2xwjF7KnFnpkqJ8MeK4h7
K5VjsLhJGhzjSsTxA8R9qE3+kGcXxXzk8xVDGJ9o8R8c
-----END RSA PRIVATE KEY-----`

func TestGetCloneUrl(t *testing.T) {
	tests := []struct {
		name          string
		user          string
		cfg           config.ServerConfig
		setupMockFunc func(*MockHTTPClient)
		expectedURL   string
		expectedError string
	}{
		{
			name: "simple token authentication",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsType:  "github",
				VcsToken: "token123",
			},
			setupMockFunc: nil, // No HTTP call needed
			expectedURL:   "https://testuser:token123@github.com",
		},
		{
			name: "custom VCS base URL",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl: "https://git.example.com",
				VcsToken:   "token123",
			},
			setupMockFunc: nil, // No HTTP call needed
			expectedURL:   "https://testuser:token123@git.example.com",
		},
		{
			name: "GitHub App missing configuration",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsType:              "github",
				VcsToken:             "token123",
				GithubAppID:          123,
				GithubInstallationID: 456,
				// Missing GithubPrivateKey
			},
			setupMockFunc: nil, // No HTTP call expected
			expectedURL:   "https://testuser:token123@github.com",
		},
		{
			name: "GitHub App successful token acquisition",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsType:              "github",
				VcsToken:             "fallback_token",
				GithubAppID:          123,
				GithubInstallationID: 456,
				GithubPrivateKey:     testPrivateKey,
			},
			setupMockFunc: func(m *MockHTTPClient) {
				responseBody := map[string]interface{}{
					"token":        "ghs_test_token",
					"expires_at":   "2024-12-31T23:59:59Z",
					"repositories": []interface{}{},
					"permissions":  map[string]interface{}{},
				}
				bodyBytes, _ := json.Marshal(responseBody)
				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
				}
				m.On("Do", mock.Anything).Return(resp, nil)
			},
			expectedURL: "https://x-access-token:ghs_test_token:fallback_token@github.com",
		},
		{
			name: "GitHub App HTTP request failure",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsType:              "github",
				VcsToken:             "fallback_token",
				GithubAppID:          123,
				GithubInstallationID: 456,
				GithubPrivateKey:     testPrivateKey,
			},
			setupMockFunc: func(m *MockHTTPClient) {
				m.On("Do", mock.Anything).Return((*http.Response)(nil), assert.AnError)
			},
			expectedError: "failed to get response",
		},
		{
			name: "custom VCS type",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsType:  "gitlab",
				VcsToken: "token123",
			},
			setupMockFunc: nil,
			expectedURL:   "https://testuser:token123@gitlab.com",
		},
		{
			name: "invalid VCS base URL",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl: "invalid://url[invalid",
				VcsToken:   "token123",
			},
			setupMockFunc: nil,
			expectedError: "failed to parse",
		},
		{
			name: "GitHub App with custom base URL",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsBaseUrl:           "https://github.enterprise.com",
				VcsType:              "github",
				VcsToken:             "fallback_token",
				GithubAppID:          123,
				GithubInstallationID: 456,
				GithubPrivateKey:     testPrivateKey,
			},
			setupMockFunc: func(m *MockHTTPClient) {
				responseBody := map[string]interface{}{
					"token":        "ghs_enterprise_token",
					"expires_at":   "2024-12-31T23:59:59Z",
					"repositories": []interface{}{},
					"permissions":  map[string]interface{}{},
				}
				bodyBytes, _ := json.Marshal(responseBody)
				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
				}
				m.On("Do", mock.Anything).Return(resp, nil)
			},
			expectedURL: "https://x-access-token:ghs_enterprise_token:fallback_token@github.enterprise.com",
		},
		{
			name: "GitHub App response with invalid JSON",
			user: "testuser",
			cfg: config.ServerConfig{
				VcsType:              "github",
				VcsToken:             "fallback_token",
				GithubAppID:          123,
				GithubInstallationID: 456,
				GithubPrivateKey:     testPrivateKey,
			},
			setupMockFunc: func(m *MockHTTPClient) {
				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewReader([]byte("invalid json"))),
				}
				m.On("Do", mock.Anything).Return(resp, nil)
			},
			expectedError: "failed to unmarshal response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockHTTPClient{}

			if tt.setupMockFunc != nil {
				tt.setupMockFunc(mockClient)
			}

			result, err := getCloneUrl(tt.user, tt.cfg, mockClient)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedURL, result)
			}

			if tt.setupMockFunc != nil {
				mockClient.AssertExpectations(t)
			}
		})
	}
}

func TestGetCloneUrl_GitHubApp_NoTokenInResponse(t *testing.T) {
	mockClient := &MockHTTPClient{}

	// Test response without token field
	responseBody := map[string]interface{}{
		"expires_at":   "2024-12-31T23:59:59Z",
		"repositories": []interface{}{},
		// No "token" field
	}
	bodyBytes, _ := json.Marshal(responseBody)

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
	}

	mockClient.On("Do", mock.Anything).Return(resp, nil)

	cfg := config.ServerConfig{
		VcsType:              "github",
		VcsToken:             "fallback_token",
		GithubAppID:          123,
		GithubInstallationID: 456,
		GithubPrivateKey:     testPrivateKey,
	}

	result, err := getCloneUrl("testuser", cfg, mockClient)
	require.NoError(t, err)

	// Should fall back to original user since no token in response
	assert.Equal(t, "https://testuser:fallback_token@github.com", result)

	mockClient.AssertExpectations(t)
}

func TestGetCloneUrl_GitHubApp_ResponseNotMap(t *testing.T) {
	mockClient := &MockHTTPClient{}

	// Test response that's not a map
	responseBody := []interface{}{"not", "a", "map"}
	bodyBytes, _ := json.Marshal(responseBody)

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
	}

	mockClient.On("Do", mock.Anything).Return(resp, nil)

	cfg := config.ServerConfig{
		VcsType:              "github",
		VcsToken:             "fallback_token",
		GithubAppID:          123,
		GithubInstallationID: 456,
		GithubPrivateKey:     testPrivateKey,
	}

	_, err := getCloneUrl("testuser", cfg, mockClient)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to convert response to map")

	mockClient.AssertExpectations(t)
}

func TestGetCloneUrl_HTTPSchemeFromBaseURL(t *testing.T) {
	mockClient := &MockHTTPClient{}

	cfg := config.ServerConfig{
		VcsBaseUrl: "http://git.internal.com",
		VcsToken:   "token123",
	}

	result, err := getCloneUrl("testuser", cfg, mockClient)
	require.NoError(t, err)

	// Should preserve the scheme from the base URL
	assert.Equal(t, "http://testuser:token123@git.internal.com", result)
}
