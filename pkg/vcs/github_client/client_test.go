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

// MockGitHubMethod is a generic function to mock GitHub client methods
func MockGitHubPullRequestMethod(methodName string, returns []interface{}) *GClient {
	mockClient := new(githubMocks.MockPullRequestsServices)
	mockClient.On(methodName, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(returns...)

	return &GClient{
		PullRequests: mockClient,
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

func TestClient_buildRepoFromComment(t *testing.T) {
	type fields struct {
		shurcoolClient *githubv4.Client
		googleClient   *GClient
		cfg            config.ServerConfig
		username       string
		email          string
	}
	type args struct {
		context context.Context
		comment *github.IssueCommentEvent
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   vcs.PullRequest
	}{
		{
			name: "normal ok",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubPullRequestMethod("Get",
					[]interface{}{
						&github.PullRequest{
							ID:     nil,
							Number: github.Int(123),
							Labels: []*github.Label{
								{
									Name: github.String("test label1"),
								},
								{
									Name: github.String("test label2"),
								},
							},
							Head: &github.PullRequestBranch{
								Ref: github.String("main"),
								SHA: github.String("dummySHA"),
							},
							Base: &github.PullRequestBranch{
								Ref: github.String("main"),
								SHA: github.String("dummySHA"),
								Repo: &github.Repository{
									CloneURL: github.String("https://github.com/zapier/kubechecks/"),
									Owner:    &github.User{Login: github.String("zapier")},
								},
							},
						},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil},
				),
				cfg:      config.ServerConfig{},
				username: "unittestuser",
				email:    "unitestuser@localhost.local",
			},
			args: args{
				context: context.TODO(),
				comment: &github.IssueCommentEvent{
					Issue: &github.Issue{
						URL: github.String("https://github.com/zapier/kubechecks/pull/250"),
					},
					Repo: &github.Repository{
						DefaultBranch: github.String("main"),
						Name:          github.String("prguy"),
						FullName:      github.String("pr guy"),
					},
				},
			},
			want: vcs.PullRequest{
				BaseRef:       "main",
				HeadRef:       "main",
				DefaultBranch: "main",
				CloneURL:      "https://github.com/zapier/kubechecks/",
				Name:          "prguy",
				Owner:         "zapier",
				CheckID:       123,
				SHA:           "dummySHA",
				FullName:      "pr guy",
				Username:      "unittestuser",
				Email:         "unitestuser@localhost.local",
				Labels:        []string{"test label1", "test label2"},
				Config:        config.ServerConfig{},
			},
		},
		{
			name: "failed to get pr data",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubPullRequestMethod("Get",
					[]interface{}{
						nil,
						&github.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}},
						fmt.Errorf("dummy error")},
				),
				cfg:      config.ServerConfig{},
				username: "unittestuser",
				email:    "unitestuser@localhost.local",
			},
			args: args{
				context: context.TODO(),
				comment: &github.IssueCommentEvent{
					Issue: &github.Issue{
						URL: github.String("https://github.com/zapier/kubechecks/pull/250"),
					},
					Repo: &github.Repository{
						DefaultBranch: github.String("main"),
						Name:          github.String("prguy"),
						FullName:      github.String("pr guy"),
					},
				},
			},
			want: nilPr,
		},
		{
			name: "missing issue pr URL",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubPullRequestMethod("Get",
					[]interface{}{
						&github.PullRequest{
							ID:     nil,
							Number: github.Int(123),
							Labels: []*github.Label{
								{
									Name: github.String("test label1"),
								},
								{
									Name: github.String("test label2"),
								},
							},
							Head: &github.PullRequestBranch{
								Ref: github.String("main"),
								SHA: github.String("dummySHA"),
							},
							Base: &github.PullRequestBranch{
								Ref: github.String("main"),
								SHA: github.String("dummySHA"),
								Repo: &github.Repository{
									CloneURL: github.String("https://github.com/zapier/kubechecks/"),
									Owner:    &github.User{Login: github.String("zapier")},
								},
							},
						},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil},
				),
				cfg:      config.ServerConfig{},
				username: "unittestuser",
				email:    "unitestuser@localhost.local",
			},
			args: args{
				context: context.TODO(),
				comment: &github.IssueCommentEvent{
					Issue: &github.Issue{
						URL: nil,
					},
					Repo: &github.Repository{
						DefaultBranch: github.String("main"),
						Name:          github.String("prguy"),
						FullName:      github.String("pr guy"),
					},
				},
			},
			want: nilPr,
		},
		{
			name: "invalid pr url",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubPullRequestMethod("Get",
					[]interface{}{
						&github.PullRequest{
							ID:     nil,
							Number: github.Int(123),
							Labels: []*github.Label{
								{
									Name: github.String("test label1"),
								},
								{
									Name: github.String("test label2"),
								},
							},
							Head: &github.PullRequestBranch{
								Ref: github.String("main"),
								SHA: github.String("dummySHA"),
							},
							Base: &github.PullRequestBranch{
								Ref: github.String("main"),
								SHA: github.String("dummySHA"),
								Repo: &github.Repository{
									CloneURL: github.String("https://github.com/zapier/kubechecks/"),
									Owner:    &github.User{Login: github.String("zapier")},
								},
							},
						},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil},
				),
				cfg:      config.ServerConfig{},
				username: "unittestuser",
				email:    "unitestuser@localhost.local",
			},
			args: args{
				context: context.TODO(),
				comment: &github.IssueCommentEvent{
					Issue: &github.Issue{
						URL: github.String("https://github.com/"),
					},
					Repo: &github.Repository{
						DefaultBranch: github.String("main"),
						Name:          github.String("prguy"),
						FullName:      github.String("pr guy"),
					},
				},
			},
			want: nilPr,
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
			assert.Equalf(t, tt.want, c.buildRepoFromComment(tt.args.context, tt.args.comment), "buildRepoFromComment(%v, %v)", tt.args.context, tt.args.comment)
		})
	}
}
