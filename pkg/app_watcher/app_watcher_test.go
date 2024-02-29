package app_watcher

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientsetfake "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/zapier/kubechecks/pkg/appdir"
)

func initTestObjects() *ApplicationWatcher {
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

	appInformer, appLister := ctrl.newApplicationInformerAndLister(time.Second * 1)
	ctrl.appInformer = appInformer
	ctrl.appLister = appLister

	return ctrl
}

func TestApplicationAdded(t *testing.T) {
	appWatcher := initTestObjects()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go appWatcher.Run(ctx, 1)

	time.Sleep(time.Second * 1)

	assert.Equal(t, len(appWatcher.vcsToArgoMap.GetMap()), 2)

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
	assert.Equal(t, len(appWatcher.vcsToArgoMap.GetMap()), 3)
}

func TestApplicationUpdated(t *testing.T) {
	ctrl := initTestObjects()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ctrl.Run(ctx, 1)

	time.Sleep(time.Second * 1)

	assert.Equal(t, len(ctrl.vcsToArgoMap.GetMap()), 2)

	oldAppDirectory := ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo.git")
	newAppDirectory := ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo-3.git")
	assert.Equal(t, oldAppDirectory.Count(), 1)
	assert.Equal(t, newAppDirectory.Count(), 0)
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
	assert.Equal(t, oldAppDirectory.Count(), 0)
	assert.Equal(t, newAppDirectory.Count(), 1)
}

func TestApplicationDeleted(t *testing.T) {
	ctrl := initTestObjects()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ctrl.Run(ctx, 1)

	time.Sleep(time.Second * 1)

	assert.Equal(t, len(ctrl.vcsToArgoMap.GetMap()), 2)

	appDirectory := ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo.git")
	assert.Equal(t, appDirectory.Count(), 1)
	//
	err := ctrl.applicationClientset.ArgoprojV1alpha1().Applications("default").Delete(ctx, "test-app-1", metav1.DeleteOptions{})
	if err != nil {
		t.Error(err)
	}
	time.Sleep(time.Second * 1)

	appDirectory = ctrl.vcsToArgoMap.GetAppsInRepo("https://gitlab.com/test/repo.git")
	assert.Equal(t, appDirectory.Count(), 0)
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
