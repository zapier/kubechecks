package app_watcher

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientsetfake "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func initTestObjectsForAppSets(t *testing.T) *ApplicationSetWatcher {
	cfg, err := config.New()
	cfg.AdditionalAppsNamespaces = []string{"*"}
	// Handle the error appropriately, e.g., log it or fail the test
	require.NoError(t, err, "failed to create config")

	// set up the fake Application client set and informer.
	testApp1 := &v1alpha1.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-1", Namespace: "default"},
		Spec: v1alpha1.ApplicationSetSpec{
			Template: v1alpha1.ApplicationSetTemplate{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: "https://gitlab.com/test/repo.git",
						Path:    "/apps/test-app-1",
					},
				},
			},
		},
	}
	testApp2 := &v1alpha1.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-2", Namespace: "default"},
		Spec: v1alpha1.ApplicationSetSpec{
			Template: v1alpha1.ApplicationSetTemplate{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: "https://github.com/test/repo.git",
						Path:    "/apps/test-app-2",
					},
				},
			},
		},
	}

	clientset := appclientsetfake.NewSimpleClientset(testApp1, testApp2)
	ctrl := &ApplicationSetWatcher{
		applicationClientset: clientset,
		vcsToArgoMap:         appdir.NewVcsToArgoMap("vcs-username"),
	}

	appInformer, appLister := ctrl.newApplicationSetInformerAndLister(time.Second*1, cfg)
	ctrl.appInformer = appInformer
	ctrl.appLister = appLister
	return ctrl
}

func TestApplicationSetWatcher_OnApplicationAdded(t *testing.T) {
	appWatcher := initTestObjectsForAppSets(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go appWatcher.Run(ctx)

	time.Sleep(time.Second * 1)

	assert.Equal(t, 2, len(appWatcher.vcsToArgoMap.GetAppSetMap()))

	_, err := appWatcher.applicationClientset.ArgoprojV1alpha1().ApplicationSets("default").Create(ctx, &v1alpha1.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-3", Namespace: "default"},
		Spec: v1alpha1.ApplicationSetSpec{
			Template: v1alpha1.ApplicationSetTemplate{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: "https://gitlab.com/test/repo-3.git",
						Path:    "apps/test-app-3",
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Error(err)
	}

	time.Sleep(time.Second * 1)
	assert.Equal(t, 3, len(appWatcher.vcsToArgoMap.GetAppSetMap()))
}

func TestApplicationSetWatcher_OnApplicationUpdated(t *testing.T) {
	ctrl := initTestObjectsForAppSets(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ctrl.Run(ctx)

	time.Sleep(time.Second * 1)

	assert.Equal(t, len(ctrl.vcsToArgoMap.GetAppSetMap()), 2)

	oldAppDirectory := ctrl.vcsToArgoMap.GetAppSetsInRepo("https://gitlab.com/test/repo.git")
	newAppDirectory := ctrl.vcsToArgoMap.GetAppSetsInRepo("https://gitlab.com/test/repo-3.git")
	assert.Equal(t, 1, oldAppDirectory.Count())
	assert.Equal(t, 0, newAppDirectory.Count())
	//
	_, err := ctrl.applicationClientset.ArgoprojV1alpha1().ApplicationSets("default").Update(ctx, &v1alpha1.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-1", Namespace: "default"},
		Spec: v1alpha1.ApplicationSetSpec{
			Template: v1alpha1.ApplicationSetTemplate{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: "https://gitlab.com/test/repo-3.git",
						Path:    "apps/test-app-3",
					},
				},
			},
		},
	}, metav1.UpdateOptions{})
	if err != nil {
		t.Error(err)
	}
	time.Sleep(time.Second * 1)
	oldAppDirectory = ctrl.vcsToArgoMap.GetAppSetsInRepo("https://gitlab.com/test/repo.git")
	newAppDirectory = ctrl.vcsToArgoMap.GetAppSetsInRepo("https://gitlab.com/test/repo-3.git")
	assert.Equal(t, oldAppDirectory.Count(), 0)
	assert.Equal(t, newAppDirectory.Count(), 1)
}

func TestApplicationSetWatcher_OnApplicationDEleted(t *testing.T) {
	ctrl := initTestObjectsForAppSets(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ctrl.Run(ctx)

	time.Sleep(time.Second * 1)

	assert.Equal(t, 2, len(ctrl.vcsToArgoMap.GetAppSetMap()))

	appDirectory := ctrl.vcsToArgoMap.GetAppSetsInRepo("https://gitlab.com/test/repo.git")
	assert.Equal(t, 1, appDirectory.Count())
	//
	err := ctrl.applicationClientset.ArgoprojV1alpha1().ApplicationSets("default").Delete(ctx, "test-app-1", metav1.DeleteOptions{})
	if err != nil {
		t.Error(err)
	}
	time.Sleep(time.Second * 1)

	appDirectory = ctrl.vcsToArgoMap.GetAppSetsInRepo("https://gitlab.com/test/repo.git")
	assert.Equal(t, 0, appDirectory.Count())
}

func Test_CanProcessAppSet(t *testing.T) {
	tests := []struct {
		name                     string
		resource                 interface{}
		expectedApp              *v1alpha1.ApplicationSet
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
			resource:      new(v1alpha1.ApplicationSet),
			expectedApp:   nil,
			returnApp:     true,
			canProcessApp: false,
		},
		{
			name: "single source without git repo",
			resource: &v1alpha1.ApplicationSet{
				Spec: v1alpha1.ApplicationSetSpec{
					Template: v1alpha1.ApplicationSetTemplate{
						Spec: v1alpha1.ApplicationSpec{
							Source: &v1alpha1.ApplicationSource{
								RepoURL: "file://../../../",
							},
						},
					},
				},
			},
			returnApp:     true,
			canProcessApp: false,
		},
		{
			name: "single source without git repo",
			resource: &v1alpha1.ApplicationSet{
				Spec: v1alpha1.ApplicationSetSpec{
					Template: v1alpha1.ApplicationSetTemplate{
						Spec: v1alpha1.ApplicationSpec{
							Source: &v1alpha1.ApplicationSource{
								RepoURL: "git@github.com:user/repo.git",
							},
						},
					},
				},
			},
			returnApp:     true,
			canProcessApp: true,
		},
		{
			name: "multi source without git repo",
			resource: &v1alpha1.ApplicationSet{
				Spec: v1alpha1.ApplicationSetSpec{
					Template: v1alpha1.ApplicationSetTemplate{
						Spec: v1alpha1.ApplicationSpec{
							Sources: v1alpha1.ApplicationSources{
								v1alpha1.ApplicationSource{
									RepoURL: "file://../../../",
								},
							},
						},
					},
				},
			},
			returnApp:     true,
			canProcessApp: false,
		},
		{
			name: "multi source with git repo",
			resource: &v1alpha1.ApplicationSet{
				Spec: v1alpha1.ApplicationSetSpec{
					Template: v1alpha1.ApplicationSetTemplate{
						Spec: v1alpha1.ApplicationSpec{
							Sources: v1alpha1.ApplicationSources{
								{
									RepoURL: "git@github.com:user/repo.git",
								},
							},
						},
					},
				},
			},
			returnApp:     true,
			canProcessApp: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			app, canProcess := canProcessAppSet(tc.resource)

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
