package gitlab_client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	gitlabMocks "github.com/zapier/kubechecks/mocks/gitlab_client/mocks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
	"gitlab.com/gitlab-org/api/client-go"
)

func TestCustomGitURLParsing(t *testing.T) {
	testcases := []struct {
		giturl, expected string
	}{
		{
			// subproject
			giturl:   "git@gitlab.com:zapier/project.git",
			expected: "zapier/project",
		},
		{
			// subproject
			giturl:   "git@gitlab.com:zapier/subteam/project.git",
			expected: "zapier/subteam/project",
		},
		{
			giturl:   "https://gitlab.com/zapier/argo-cd-configs.git",
			expected: "zapier/argo-cd-configs",
		},
		{
			// custom domain
			giturl:   "git@git.mycompany.com:k8s/namespaces/security",
			expected: "k8s/namespaces/security",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.giturl, func(t *testing.T) {
			actual, err := parseRepoName(tc.giturl)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestClient_GetHookByUrl(t *testing.T) {
	type fields struct {
		c        *GLClient
		cfg      config.ServerConfig
		username string
		email    string
	}
	type args struct {
		ctx        context.Context
		repoName   string
		webhookUrl string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *vcs.WebHookConfig
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "normal ok",
			fields: fields{
				c: MockGitLabProjects("ListProjectHooks",
					[]interface{}{
						[]*gitlab.ProjectHook{
							{
								URL:                 "https://dummywebhooks.local",
								MergeRequestsEvents: true,
								NoteEvents:          true,
							},
						},
						&gitlab.Response{
							Response: &http.Response{StatusCode: http.StatusOK},
						},
						nil,
					}),
				cfg:      config.ServerConfig{},
				username: "",
				email:    "",
			},
			args: args{
				ctx:        context.TODO(),
				repoName:   "test",
				webhookUrl: "https://dummywebhooks.local",
			},
			want: &vcs.WebHookConfig{
				Url:    "https://dummywebhooks.local",
				Events: []string{"merge_request", "note"},
			},
			wantErr: assert.NoError,
		},
		{
			name: "gl client err",
			fields: fields{
				c: MockGitLabProjects("ListProjectHooks",
					[]interface{}{
						nil,
						&gitlab.Response{
							Response: &http.Response{StatusCode: http.StatusInternalServerError},
						},
						fmt.Errorf("dummy error"),
					}),
				cfg:      config.ServerConfig{},
				username: "",
				email:    "",
			},
			args: args{
				ctx:        context.TODO(),
				repoName:   "test",
				webhookUrl: "https://dummywebhooks.local",
			},
			want:    nil,
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				c:        tt.fields.c,
				cfg:      tt.fields.cfg,
				username: tt.fields.username,
				email:    tt.fields.email,
			}
			got, err := c.GetHookByUrl(tt.args.ctx, tt.args.repoName, tt.args.webhookUrl)
			if !tt.wantErr(t, err, fmt.Sprintf("GetHookByUrl(%v, %v, %v)", tt.args.ctx, tt.args.repoName, tt.args.webhookUrl)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetHookByUrl(%v, %v, %v)", tt.args.ctx, tt.args.repoName, tt.args.webhookUrl)
		})
	}
}

// MockGitLabProjects is a generic function to mock Gitlab MergeRequest client methods
func MockGitLabProjects(methodName string, returns []interface{}) *GLClient {
	mockClient := new(gitlabMocks.MockProjectsServices)
	mockClient.On(methodName, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(returns...)

	return &GLClient{
		Projects: mockClient,
	}
}

func TestClient_CreateHook(t *testing.T) {
	type fields struct {
		c        *GLClient
		cfg      config.ServerConfig
		username string
		email    string
	}
	type args struct {
		ctx           context.Context
		repoName      string
		webhookUrl    string
		webhookSecret string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "normal ok",
			fields: fields{
				c: MockGitLabProjects("AddProjectHook",
					[]interface{}{
						&gitlab.ProjectHook{
							URL:                 "https://dummywebhooks.local",
							MergeRequestsEvents: true,
							NoteEvents:          true,
						},
						&gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil,
					}),
				cfg:      config.ServerConfig{},
				username: "",
				email:    "",
			},
			args: args{
				ctx:           context.TODO(),
				repoName:      "main",
				webhookUrl:    "https://dummywebhooks.local",
				webhookSecret: "",
			},
			wantErr: assert.NoError,
		},
		{
			name: "gitlab error",
			fields: fields{
				c: MockGitLabProjects("AddProjectHook",
					[]interface{}{
						nil,
						&gitlab.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}},
						fmt.Errorf("dummy error"),
					}),
				cfg:      config.ServerConfig{},
				username: "",
				email:    "",
			},
			args: args{
				ctx:           context.TODO(),
				repoName:      "main",
				webhookUrl:    "https://dummywebhooks.local",
				webhookSecret: "",
			},
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				c:        tt.fields.c,
				cfg:      tt.fields.cfg,
				username: tt.fields.username,
				email:    tt.fields.email,
			}
			tt.wantErr(t, c.CreateHook(tt.args.ctx, tt.args.repoName, tt.args.webhookUrl, tt.args.webhookSecret), fmt.Sprintf("CreateHook(%v, %v, %v, %v)", tt.args.ctx, tt.args.repoName, tt.args.webhookUrl, tt.args.webhookSecret))
		})
	}
}

func TestClient_SimpleGetters(t *testing.T) {
	c := &Client{
		cfg:      config.ServerConfig{},
		username: "test-user",
		email:    "test@example.com",
	}

	assert.Equal(t, "test-user", c.Username())
	assert.Equal(t, "test@example.com", c.Email())
	assert.Equal(t, "test-user", c.CloneUsername())
	assert.Equal(t, "gitlab", c.GetName())
}

func TestClient_GetAuthHeaders(t *testing.T) {
	c := &Client{
		cfg: config.ServerConfig{
			VcsToken: "test-token-12345",
		},
	}

	headers := c.GetAuthHeaders()
	assert.Equal(t, "test-token-12345", headers["PRIVATE-TOKEN"])
}

func TestClient_VerifyHook(t *testing.T) {
	tests := []struct {
		name        string
		secret      string
		headerToken string
		wantErr     bool
		errMsg      string
	}{
		{
			name:        "valid secret",
			secret:      "my-secret",
			headerToken: "my-secret",
			wantErr:     false,
		},
		{
			name:        "invalid secret",
			secret:      "my-secret",
			headerToken: "wrong-secret",
			wantErr:     true,
			errMsg:      "invalid secret",
		},
		{
			name:        "no secret configured",
			secret:      "",
			headerToken: "any-token",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{}
			req := &http.Request{
				Header: http.Header{
					GitlabTokenHeader: []string{tt.headerToken},
				},
				Body: io.NopCloser(strings.NewReader("test body")),
			}

			body, err := c.VerifyHook(req, tt.secret)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, "test body", string(body))
			}
		})
	}
}

// Note: buildRepoFromEvent and buildRepoFromComment use complex GitLab SDK structs
// that have embedded anonymous structs which are difficult to mock properly.
// These functions are best tested via integration tests or by testing ParseHook
// which calls them internally.

func TestClient_checkMRReadiness(t *testing.T) {
	tests := []struct {
		name               string
		projectID          interface{}
		mrIID              int
		httpStatusCode     int
		expectedStatus     MRReadinessStatus
		expectedDetailed   string
		expectedReasonPart string // substring to check in reason
	}{
		{
			name:               "MR is ready - HTTP 200",
			projectID:          "test-org/test-repo",
			mrIID:              123,
			httpStatusCode:     http.StatusOK,
			expectedStatus:     mrReady,
			expectedDetailed:   "mergeable",
			expectedReasonPart: "HTTP 200",
		},
		{
			name:               "MR has conflicts - HTTP 400",
			projectID:          "test-org/test-repo",
			mrIID:              123,
			httpStatusCode:     http.StatusBadRequest,
			expectedStatus:     mrFailed,
			expectedDetailed:   "not_mergeable",
			expectedReasonPart: "HTTP 400",
		},
		{
			name:               "Merge ref not ready - HTTP 404",
			projectID:          "test-org/test-repo",
			mrIID:              123,
			httpStatusCode:     http.StatusNotFound,
			expectedStatus:     mrTransient,
			expectedDetailed:   "merge_ref_not_ready",
			expectedReasonPart: "HTTP 404",
		},
		{
			name:               "Rate limited - HTTP 429",
			projectID:          "test-org/test-repo",
			mrIID:              123,
			httpStatusCode:     http.StatusTooManyRequests,
			expectedStatus:     mrTransient,
			expectedDetailed:   "rate_limited",
			expectedReasonPart: "HTTP 429",
		},
		{
			name:               "Other HTTP error - HTTP 500",
			projectID:          "test-org/test-repo",
			mrIID:              123,
			httpStatusCode:     http.StatusInternalServerError,
			expectedStatus:     mrFailed,
			expectedDetailed:   "http_500",
			expectedReasonPart: "HTTP 500",
		},
		{
			name:               "Project ID with slashes gets encoded",
			projectID:          "org/subteam/project",
			mrIID:              456,
			httpStatusCode:     http.StatusOK,
			expectedStatus:     mrReady,
			expectedDetailed:   "mergeable",
			expectedReasonPart: "HTTP 200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test HTTP server that returns the desired status code
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify the request path (note: URL path is decoded by the HTTP server)
				expectedPath := fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d/merge_ref",
					fmt.Sprintf("%v", tt.projectID),
					tt.mrIID)

				assert.Equal(t, expectedPath, r.URL.Path, "Request path should match expected format")
				assert.Equal(t, "GET", r.Method, "Request method should be GET")

				// Return the configured status code
				w.WriteHeader(tt.httpStatusCode)
				if tt.httpStatusCode == http.StatusOK {
					// Return a minimal valid response for 200 OK
					_, _ = w.Write([]byte(`{"commit_id":"abc123"}`))
				}
			})

			server := httptest.NewServer(handler)
			defer server.Close()

			// Create a GitLab client pointing to the test server
			glClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(server.URL+"/api/v4"))
			require.NoError(t, err, "Failed to create GitLab client")

			// Wrap in our Client struct
			client := &Client{
				c: &GLClient{
					Client: glClient,
				},
				cfg: config.ServerConfig{},
			}

			// Call checkMRReadiness
			ctx := context.Background()
			result := client.checkMRReadiness(ctx, tt.projectID, tt.mrIID)

			// Verify results
			assert.Equal(t, tt.expectedStatus, result.Status, "Status should match")
			assert.Equal(t, tt.expectedDetailed, result.DetailedStatus, "Detailed status should match")
			assert.Contains(t, result.Reason, tt.expectedReasonPart, "Reason should contain expected substring")
		})
	}
}

// Note: ParseHook testing requires actual GitLab webhook JSON payloads
// and is best tested via integration tests with real webhook data.

// MockMergeRequestsService is a simple mock for testing
type MockMergeRequestsService struct {
	mock.Mock
}

func (m *MockMergeRequestsService) GetMergeRequestDiffVersions(pid interface{}, mergeRequest int, opt *gitlab.GetMergeRequestDiffVersionsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.MergeRequestDiffVersion, *gitlab.Response, error) {
	args := m.Called(pid, mergeRequest, opt, options)
	if args.Get(0) == nil {
		return nil, args.Get(1).(*gitlab.Response), args.Error(2)
	}
	return args.Get(0).([]*gitlab.MergeRequestDiffVersion), args.Get(1).(*gitlab.Response), args.Error(2)
}

func (m *MockMergeRequestsService) ListMergeRequestDiffs(pid interface{}, mergeRequest int, opt *gitlab.ListMergeRequestDiffsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.MergeRequestDiff, *gitlab.Response, error) {
	args := m.Called(pid, mergeRequest, opt, options)
	if args.Get(0) == nil {
		return nil, args.Get(1).(*gitlab.Response), args.Error(2)
	}
	return args.Get(0).([]*gitlab.MergeRequestDiff), args.Get(1).(*gitlab.Response), args.Error(2)
}

func (m *MockMergeRequestsService) UpdateMergeRequest(pid interface{}, mergeRequest int, opt *gitlab.UpdateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
	args := m.Called(pid, mergeRequest, opt, options)
	if args.Get(0) == nil {
		return nil, args.Get(1).(*gitlab.Response), args.Error(2)
	}
	return args.Get(0).(*gitlab.MergeRequest), args.Get(1).(*gitlab.Response), args.Error(2)
}

func (m *MockMergeRequestsService) GetMergeRequest(pid interface{}, mergeRequest int, opt *gitlab.GetMergeRequestsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
	args := m.Called(pid, mergeRequest, opt, options)
	if args.Get(0) == nil {
		return nil, args.Get(1).(*gitlab.Response), args.Error(2)
	}
	return args.Get(0).(*gitlab.MergeRequest), args.Get(1).(*gitlab.Response), args.Error(2)
}

func TestClient_GetPullRequestFiles(t *testing.T) {
	// Mock the MergeRequests service
	mockMR := new(MockMergeRequestsService)
	mockMR.On("ListMergeRequestDiffs",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything).Return(
		[]*gitlab.MergeRequestDiff{
			{
				OldPath: "old-file.txt",
				NewPath: "new-file.txt",
			},
			{
				OldPath: "deleted-file.txt",
				NewPath: "/dev/null",
			},
			{
				OldPath: "/dev/null",
				NewPath: "added-file.txt",
			},
		},
		&gitlab.Response{},
		nil,
	)

	c := &Client{
		c: &GLClient{
			MergeRequests: mockMR,
		},
		cfg:      config.ServerConfig{},
		username: "test-user",
		email:    "test@example.com",
	}

	pr := vcs.PullRequest{
		FullName: "test/repo",
		CheckID:  123,
	}

	files, err := c.GetPullRequestFiles(context.Background(), pr)
	assert.NoError(t, err)
	assert.Contains(t, files, "new-file.txt")
	assert.Contains(t, files, "deleted-file.txt")
	assert.Contains(t, files, "added-file.txt")
	assert.NotContains(t, files, "/dev/null")
}

func TestClient_LoadHook(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "invalid format - missing !",
			id:      "org/repo/123",
			wantErr: true,
		},
		{
			name:    "invalid format - invalid number",
			id:      "org/repo!abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				c:        &GLClient{},
				cfg:      config.ServerConfig{},
				username: "test-user",
				email:    "test@example.com",
			}

			_, err := c.LoadHook(context.Background(), tt.id)
			if tt.wantErr {
				assert.Error(t, err)
			}
		})
	}
}

func TestCreateGitlabClient_NoToken(t *testing.T) {
	_, err := CreateGitlabClient(context.Background(), config.ServerConfig{
		VcsToken: "",
	})
	assert.Equal(t, ErrNoToken, err)
}
