package appdir

import (
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestAddApp tests the AddApp method from the VcsToArgoMap type.
func TestAddApp(t *testing.T) {
	// Setup your mocks and expected calls here.

	v2a := NewVcsToArgoMap() // This would be mocked accordingly.
	app1 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-1", Namespace: "default"},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{
				RepoURL: "https://github.com/argoproj/argo-cd.git",
				Path:    "test-app-1",
			},
		},
	}

	v2a.AddApp(app1)
	appDir := v2a.GetAppsInRepo("https://github.com/argoproj/argo-cd.git")

	assert.Equal(t, appDir.Count(), 1)
	assert.Equal(t, len(appDir.appDirs["test-app-1"]), 1)

	// Assertions to verify the behavior here.
	app2 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-2", Namespace: "default"},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{
				RepoURL: "https://github.com/argoproj/argo-cd.git",
				Path:    "test-app-2",
			},
		},
	}

	v2a.AddApp(app2)
	assert.Equal(t, appDir.Count(), 2)
	assert.Equal(t, len(appDir.appDirs["test-app-2"]), 1)
}

func TestDeleteApp(t *testing.T) {
	// Setup your mocks and expected calls here.

	v2a := NewVcsToArgoMap() // This would be mocked accordingly.
	app1 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-1", Namespace: "default"},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{
				RepoURL: "https://github.com/argoproj/argo-cd.git",
				Path:    "test-app-1",
			},
		},
	}
	// Assertions to verify the behavior here.
	app2 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app-2", Namespace: "default"},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{
				RepoURL: "https://github.com/argoproj/argo-cd.git",
				Path:    "test-app-2",
			},
		},
	}

	v2a.AddApp(app1)
	v2a.AddApp(app2)
	appDir := v2a.GetAppsInRepo("https://github.com/argoproj/argo-cd.git")

	assert.Equal(t, appDir.Count(), 2)
	assert.Equal(t, len(appDir.appDirs["test-app-1"]), 1)
	assert.Equal(t, len(appDir.appDirs["test-app-2"]), 1)

	v2a.DeleteApp(app2)
	assert.Equal(t, appDir.Count(), 1)
	assert.Equal(t, len(appDir.appDirs["test-app-2"]), 0)
}
