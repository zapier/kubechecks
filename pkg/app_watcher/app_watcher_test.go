package app_watcher

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientsetfake "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/config"
)

func initTestObjects(t *testing.T) *ApplicationWatcher {
	t.Helper()

	cfg, err := config.New()
	// Handle the error appropriately, e.g., log it or fail the test
	require.NoError(t, err, "failed to create config")

	// set up the fake Application client set and informer.
	testApp1 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-1", Namespace: "default"},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{RepoURL: "https://gitlab.com/test/repo.git"},
		},
	}
	testApp2 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-2", Namespace: "default"},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{RepoURL: "https://github.com/test/repo.git"},
		},
	}

	clientset := appclientsetfake.NewSimpleClientset(testApp1, testApp2)
	ctrl := &ApplicationWatcher{
		applicationClientset: clientset,
		vcsToArgoMap:         appdir.NewVcsToArgoMap("vcs-username"),
	}

	appInformer, appLister := ctrl.newApplicationInformerAndLister(time.Second*1, cfg)
	ctrl.appInformer = appInformer
	ctrl.appLister = appLister

	return ctrl
}

func TestApplicationAdded(t *testing.T) {
	appWatcher := initTestObjects(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go appWatcher.Run(ctx, 1)

	time.Sleep(time.Second * 1)

	assert.Len(t, appWatcher.vcsToArgoMap.GetMap(), 2)

	_, err := appWatcher.applicationClientset.ArgoprojV1alpha1().Applications("default").Create(ctx, &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-3", Namespace: "default"},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{RepoURL: "https://gitlab.com/test/repo-3.git"},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Error(err)
	}

	time.Sleep(time.Second * 1)
	assert.Len(t, appWatcher.vcsToArgoMap.GetMap(), 3)
}

func TestApplicationUpdated(t *testing.T) {
	ctrl := initTestObjects(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ctrl.Run(ctx, 1)

	time.Sleep(time.Second * 1)

	assert.Len(t, ctrl.vcsToArgoMap.GetMap(), 2)

	oldAppDirectory := ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo.git")
	newAppDirectory := ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo-3.git")
	assert.Equal(t, 1, oldAppDirectory.Count())
	assert.Equal(t, 0, newAppDirectory.Count())
	//
	_, err := ctrl.applicationClientset.ArgoprojV1alpha1().Applications("default").Update(ctx, &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-1", Namespace: "default"},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{RepoURL: "https://gitlab.com/test/repo-3.git"},
		},
	}, metav1.UpdateOptions{})
	if err != nil {
		t.Error(err)
	}
	time.Sleep(time.Second * 1)
	oldAppDirectory = ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo.git")
	newAppDirectory = ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo-3.git")
	assert.Equal(t, 0, oldAppDirectory.Count())
	assert.Equal(t, 1, newAppDirectory.Count())
}

func TestApplicationDeleted(t *testing.T) {
	ctrl := initTestObjects(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ctrl.Run(ctx, 1)

	time.Sleep(time.Second * 1)

	assert.Len(t, ctrl.vcsToArgoMap.GetMap(), 2)

	appDirectory := ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo.git")
	assert.Equal(t, 1, appDirectory.Count())
	//
	err := ctrl.applicationClientset.ArgoprojV1alpha1().Applications("default").Delete(ctx, "test-app-1", metav1.DeleteOptions{})
	if err != nil {
		t.Error(err)
	}
	time.Sleep(time.Second * 1)

	appDirectory = ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo.git")
	assert.Equal(t, 0, appDirectory.Count())
}

// TestIsGitRepo will test various URLs against the isGitRepo function.
func TestIsGitRepo(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://github.com/user/repo.git", true},
		{"https://gitlab.com/user/repo.git", true},
		{"ssh://gitlab.com/user/repo.git", true},
		{"user@github.com:user/repo.git", true},
		{"https://bitbucket.org/user/repo.git", false},
		{"user@gitlab.invalid/user/repo.git", false},
		{"http://myownserver.com/git/repo.git", false},
	}

	for _, test := range tests {
		if result := isGitRepo(test.url); result != test.expected {
			t.Errorf("isGitRepo(%q) = %v; want %v", test.url, result, test.expected)
		}
	}
}

func TestCanProcessApp(t *testing.T) {
	tests := []struct {
		name                     string
		resource                 interface{}
		expectedApp              *v1alpha1.Application
		returnApp, canProcessApp bool
	}{
		{
			name:          "nil resource",
			resource:      nil,
			expectedApp:   nil,
			returnApp:     false,
			canProcessApp: false,
		},
		{
			name:          "not an app",
			resource:      new(string),
			expectedApp:   nil,
			returnApp:     false,
			canProcessApp: false,
		},
		{
			name:          "empty app",
			resource:      new(v1alpha1.Application),
			expectedApp:   nil,
			returnApp:     true,
			canProcessApp: false,
		},
		{
			name: "single source without git repo",
			resource: &v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: "file://../../../",
					},
				},
			},
			returnApp:     true,
			canProcessApp: false,
		},
		{
			name: "single source without git repo",
			resource: &v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: "git@github.com:user/repo.git",
					},
				},
			},
			returnApp:     true,
			canProcessApp: true,
		},
		{
			name: "multi source without git repo",
			resource: &v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Sources: v1alpha1.ApplicationSources{
						{
							RepoURL: "file://../../../",
						},
					},
				},
			},
			returnApp:     true,
			canProcessApp: false,
		},
		{
			name: "multi source with git repo",
			resource: &v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Sources: v1alpha1.ApplicationSources{
						{
							RepoURL: "git@github.com:user/repo.git",
						},
					},
				},
			},
			returnApp:     true,
			canProcessApp: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, canProcess := canProcessApp(tc.resource)

			if tc.canProcessApp {
				assert.True(t, canProcess)
			} else {
				assert.False(t, canProcess)
			}

			if tc.returnApp {
				assert.Equal(t, tc.resource, app)
			} else {
				assert.Nil(t, app)
			}
		})
	}
}
