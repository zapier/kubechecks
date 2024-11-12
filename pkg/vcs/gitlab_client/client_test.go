package gitlab_client

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/xanzy/go-gitlab"
	gitlabMocks "github.com/zapier/kubechecks/mocks/gitlab_client/mocks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
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
