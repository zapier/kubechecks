package events

import (
	"context"
	"fmt"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	affectedappsmocks "github.com/zapier/kubechecks/mocks/affected_apps/mocks"
	generatorsmocks "github.com/zapier/kubechecks/mocks/generator/mocks"
	vcsmocks "github.com/zapier/kubechecks/mocks/vcs/mocks"
	"github.com/zapier/kubechecks/pkg/affected_apps"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/generator"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCheckEventGetRepo(t *testing.T) {
	cloneURL := "https://github.com/zapier/kubechecks.git"
	canonical, err := canonicalize(cloneURL)
	cfg := config.ServerConfig{}
	require.NoError(t, err)

	ctx := context.TODO()

	t.Run("empty branch name", func(t *testing.T) {
		vcsClient := new(vcsmocks.MockClient)
		vcsClient.EXPECT().Username().Return("username")

		ce := CheckEvent{
			clonedRepos: make(map[repoKey]*git.Repo),
			repoManager: git.NewRepoManager(cfg),
			ctr:         container.Container{VcsClient: vcsClient},
		}

		repo, err := ce.getRepo(ctx, cloneURL, "")
		require.NoError(t, err)
		assert.Equal(t, "main", repo.BranchName)
		assert.Len(t, ce.clonedRepos, 2)
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "HEAD"))
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "main"))
	})

	t.Run("branch is HEAD", func(t *testing.T) {
		vcsClient := new(vcsmocks.MockClient)
		vcsClient.EXPECT().Username().Return("username")

		ce := CheckEvent{
			clonedRepos: make(map[repoKey]*git.Repo),
			repoManager: git.NewRepoManager(cfg),
			ctr:         container.Container{VcsClient: vcsClient},
		}

		repo, err := ce.getRepo(ctx, cloneURL, "HEAD")
		require.NoError(t, err)
		assert.Equal(t, "main", repo.BranchName)
		assert.Len(t, ce.clonedRepos, 2)
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "HEAD"))
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "main"))
	})

	t.Run("branch is the same as HEAD", func(t *testing.T) {
		vcsClient := new(vcsmocks.MockClient)
		vcsClient.EXPECT().Username().Return("username")

		ce := CheckEvent{
			clonedRepos: make(map[repoKey]*git.Repo),
			repoManager: git.NewRepoManager(cfg),
			ctr:         container.Container{VcsClient: vcsClient},
		}

		repo, err := ce.getRepo(ctx, cloneURL, "main")
		require.NoError(t, err)
		assert.Equal(t, "main", repo.BranchName)
		assert.Len(t, ce.clonedRepos, 2)
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "HEAD"))
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "main"))
	})

	t.Run("branch is not the same as HEAD", func(t *testing.T) {
		vcsClient := new(vcsmocks.MockClient)
		vcsClient.EXPECT().Username().Return("username")

		ce := CheckEvent{
			clonedRepos: make(map[repoKey]*git.Repo),
			repoManager: git.NewRepoManager(cfg),
			ctr:         container.Container{VcsClient: vcsClient},
		}

		repo, err := ce.getRepo(ctx, cloneURL, "gh-pages")
		require.NoError(t, err)
		assert.Equal(t, "gh-pages", repo.BranchName)
		assert.Len(t, ce.clonedRepos, 1)
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "gh-pages"))
	})
}

func TestCheckEvent_GenerateListOfAffectedApps(t *testing.T) {
	type fields struct {
		fileList      []string
		pullRequest   vcs.PullRequest
		logger        zerolog.Logger
		vcsNote       *msg.Message
		affectedItems affected_apps.AffectedItems
		ctr           container.Container
		repoManager   repoManager
		processors    []checks.ProcessorEntry
		clonedRepos   map[repoKey]*git.Repo
		addedAppsSet  map[string]v1alpha1.Application
		appsSent      int32
		appChannel    chan *v1alpha1.Application
		generator     generator.AppsGenerator
		matcher       affected_apps.Matcher
	}
	type args struct {
		ctx           context.Context
		repo          *git.Repo
		targetBranch  string
		initMatcherFn MatcherFn
	}
	tests := []struct {
		name             string
		fields           fields
		args             args
		expectedAppCount int
		wantErr          assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
		{
			name: "no error",
			fields: fields{
				fileList:      nil,
				pullRequest:   vcs.PullRequest{},
				logger:        zerolog.Logger{},
				vcsNote:       nil,
				affectedItems: affected_apps.AffectedItems{},
				ctr:           container.Container{},
				repoManager:   nil,
				processors:    nil,
				clonedRepos:   nil,
				addedAppsSet:  nil,
				appsSent:      0,
				appChannel:    nil,
				generator:     MockGenerator("GenerateApplicationSetApps", []interface{}{[]v1alpha1.Application{}, nil}),
				matcher: MockMatcher("AffectedApps", []interface{}{
					affected_apps.AffectedItems{
						ApplicationSets: []v1alpha1.ApplicationSet{
							{
								TypeMeta:   metav1.TypeMeta{Kind: "ApplicationSet", APIVersion: "argoproj.io/v1alpha1"},
								ObjectMeta: metav1.ObjectMeta{Name: "appset1"},
							},
						},
					},
					nil,
				}),
			},
			args: args{
				ctx:           context.Background(),
				repo:          &git.Repo{Directory: "/tmp"},
				targetBranch:  "HEAD",
				initMatcherFn: MockInitMatcherFn(),
			},
			expectedAppCount: 1,
			wantErr:          assert.NoError,
		},
		{
			name: "matcher error",
			fields: fields{
				fileList:      nil,
				pullRequest:   vcs.PullRequest{},
				logger:        zerolog.Logger{},
				vcsNote:       nil,
				affectedItems: affected_apps.AffectedItems{},
				ctr:           container.Container{},
				repoManager:   nil,
				processors:    nil,
				clonedRepos:   nil,
				addedAppsSet:  nil,
				appsSent:      0,
				appChannel:    nil,
				generator:     MockGenerator("GenerateApplicationSetApps", []interface{}{[]v1alpha1.Application{}, nil}),
				matcher: MockMatcher("AffectedApps", []interface{}{
					affected_apps.AffectedItems{},
					fmt.Errorf("mock error"),
				}),
			},
			args: args{
				ctx:           context.Background(),
				repo:          &git.Repo{Directory: "/tmp"},
				targetBranch:  "HEAD",
				initMatcherFn: MockInitMatcherFn(),
			},
			expectedAppCount: 0,
			wantErr:          assert.Error,
		},
		{
			name: "generator error",
			fields: fields{
				fileList:      nil,
				pullRequest:   vcs.PullRequest{},
				logger:        zerolog.Logger{},
				vcsNote:       nil,
				affectedItems: affected_apps.AffectedItems{},
				ctr:           container.Container{},
				repoManager:   nil,
				processors:    nil,
				clonedRepos:   nil,
				addedAppsSet:  nil,
				appsSent:      0,
				appChannel:    nil,
				generator:     MockGenerator("GenerateApplicationSetApps", []interface{}{[]v1alpha1.Application{}, fmt.Errorf("mock error")}),
				matcher: MockMatcher("AffectedApps", []interface{}{
					affected_apps.AffectedItems{
						ApplicationSets: []v1alpha1.ApplicationSet{
							{
								TypeMeta:   metav1.TypeMeta{Kind: "ApplicationSet", APIVersion: "argoproj.io/v1alpha1"},
								ObjectMeta: metav1.ObjectMeta{Name: "appset1"},
							},
						},
					},
					nil,
				}),
			},
			args: args{
				ctx:           context.Background(),
				repo:          &git.Repo{Directory: "/tmp"},
				targetBranch:  "HEAD",
				initMatcherFn: MockInitMatcherFn(),
			},
			expectedAppCount: 0,
			wantErr:          assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ce := &CheckEvent{
				fileList:      tt.fields.fileList,
				pullRequest:   tt.fields.pullRequest,
				logger:        tt.fields.logger,
				vcsNote:       tt.fields.vcsNote,
				affectedItems: tt.fields.affectedItems,
				ctr:           tt.fields.ctr,
				repoManager:   tt.fields.repoManager,
				processors:    tt.fields.processors,
				clonedRepos:   tt.fields.clonedRepos,
				addedAppsSet:  tt.fields.addedAppsSet,
				appsSent:      tt.fields.appsSent,
				appChannel:    tt.fields.appChannel,
				generator:     tt.fields.generator,
				matcher:       tt.fields.matcher,
			}
			tt.wantErr(t, ce.GenerateListOfAffectedApps(tt.args.ctx, tt.args.repo, tt.args.targetBranch, tt.args.initMatcherFn), fmt.Sprintf("GenerateListOfAffectedApps(%v, %v, %v, %v)", tt.args.ctx, tt.args.repo, tt.args.targetBranch, tt.args.initMatcherFn))

		})
	}
}

func MockMatcher(methodName string, returns []interface{}) affected_apps.Matcher {
	mockClient := new(affectedappsmocks.MockMatcher)
	mockClient.On(methodName, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(returns...)

	return mockClient
}

func MockGenerator(methodName string, returns []interface{}) generator.AppsGenerator {
	mockClient := new(generatorsmocks.MockAppsGenerator)
	mockClient.On(methodName, mock.Anything, mock.Anything, mock.Anything).Return(returns...)

	return mockClient
}

func MockInitMatcherFn() MatcherFn {
	return func(ce *CheckEvent, repo *git.Repo) error {
		return nil
	}
}
