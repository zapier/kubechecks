package github_client

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-github/v62/github"
	"github.com/shurcooL/githubv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	githubMocks "github.com/zapier/kubechecks/mocks/github_client/mocks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

// MockGitHubMethod is a generic function to mock GitHub client methods
func MockGitHubMethod(methodName string, returns []interface{}) *GClient {
	mockClient := new(githubMocks.MockRepositoriesServices)
	mockClient.On(methodName, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(returns...)

	return &GClient{
		Repositories: mockClient,
	}
}

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

func TestClient_CreateHook(t *testing.T) {
	type fields struct {
		shurcoolClient *githubv4.Client
		googleClient   *GClient
		cfg            config.ServerConfig
		username       string
		email          string
	}
	type args struct {
		ctx              context.Context
		ownerAndRepoName string
		webhookUrl       string
		webhookSecret    string
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
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("CreateHook",
					[]interface{}{
						&github.Hook{},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
				webhookSecret:    "dummy-webhook-secret",
			},
			wantErr: assert.NoError,
		},
		{
			name: "github responds with error",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("CreateHook",
					[]interface{}{
						nil,
						&github.Response{Response: &http.Response{StatusCode: http.StatusBadRequest}},
						fmt.Errorf("mock bad request")}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
				webhookSecret:    "dummy-webhook-secret",
			},
			wantErr: assert.Error,
		},
		{
			name: "mock network error error",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("CreateHook",
					[]interface{}{
						nil,
						nil,
						fmt.Errorf("mock network error")}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
				webhookSecret:    "dummy-webhook-secret",
			},
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				shurcoolClient: tt.fields.shurcoolClient,
				googleClient:   tt.fields.googleClient,
				cfg:            tt.fields.cfg,
				username:       tt.fields.username,
				email:          tt.fields.email,
			}
			tt.wantErr(t, c.CreateHook(tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl, tt.args.webhookSecret), fmt.Sprintf("CreateHook(%v, %v, %v, %v)", tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl, tt.args.webhookSecret))
		})
	}
}

func TestClient_GetHookByUrl(t *testing.T) {
	type fields struct {
		shurcoolClient *githubv4.Client
		googleClient   *GClient
		cfg            config.ServerConfig
		username       string
		email          string
	}
	type args struct {
		ctx              context.Context
		ownerAndRepoName string
		webhookUrl       string
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
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("ListHooks",
					[]interface{}{
						[]*github.Hook{
							{
								Config: &github.HookConfig{
									ContentType: github.String("json"),
									InsecureSSL: github.String("0"),
									URL:         github.String("https://dummywebhooks.local"),
									Secret:      github.String("dummy-webhook-secret"),
								},
								Events: []string{"pull_request"},
							},
						},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
			},
			want: &vcs.WebHookConfig{
				Url:    "https://dummywebhooks.local",
				Events: []string{"pull_request"},
			},
			wantErr: assert.NoError,
		},
		{
			name: "no matching webhook found",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("ListHooks",
					[]interface{}{
						[]*github.Hook{
							{
								Config: &github.HookConfig{
									ContentType: github.String("json"),
									InsecureSSL: github.String("0"),
									URL:         github.String("https://differentwebhook.local"),
									Secret:      github.String("dummy-webhook-secret"),
								},
								Events: []string{"pull_request"},
							},
						},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
			},
			want:    nil,
			wantErr: assert.Error,
		},
		{
			name: "0 webhook found",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("ListHooks",
					[]interface{}{
						nil,
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
			},
			want:    nil,
			wantErr: assert.Error,
		},
		{
			name: "github error",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("ListHooks",
					[]interface{}{
						nil,
						&github.Response{Response: &http.Response{StatusCode: http.StatusBadRequest}},
						fmt.Errorf("mock bad request")}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
			},
			want:    nil,
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				shurcoolClient: tt.fields.shurcoolClient,
				googleClient:   tt.fields.googleClient,
				cfg:            tt.fields.cfg,
				username:       tt.fields.username,
				email:          tt.fields.email,
			}
			got, err := c.GetHookByUrl(tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl)
			if !tt.wantErr(t, err, fmt.Sprintf("GetHookByUrl(%v, %v, %v)", tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetHookByUrl(%v, %v, %v)", tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl)
		})
	}
}
