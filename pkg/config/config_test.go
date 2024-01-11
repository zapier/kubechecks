package config

import (
	"fmt"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNormalizeStrings(t *testing.T) {
	testCases := []struct {
		input    string
		expected RepoURL
	}{
		{
			input:    "git@github.com:one/two",
			expected: RepoURL{"github.com", "one/two"},
		},
		{
			input:    "https://github.com/one/two",
			expected: RepoURL{"github.com", "one/two"},
		},
		{
			input:    "git@gitlab.com:djeebus/helm-test.git",
			expected: RepoURL{"gitlab.com", "djeebus/helm-test"},
		},
		{
			input:    "https://gitlab.com/djeebus/helm-test.git",
			expected: RepoURL{"gitlab.com", "djeebus/helm-test"},
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("case %s", tc.input), func(t *testing.T) {
			actual, err := NormalizeRepoUrl(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

// TestBuildNormalizedRepoURL tests the buildNormalizedRepoUrl function.
func TestBuildNormalizedRepoURL(t *testing.T) {
	tests := []struct {
		host     string
		path     string
		expected RepoURL
	}{
		{
			host: "example.com",
			path: "/repository.git",
			expected: RepoURL{
				Host: "example.com",
				Path: "repository",
			},
		},
		// ... additional test cases
	}

	for _, tc := range tests {
		result := buildNormalizedRepoUrl(tc.host, tc.path)
		assert.Equal(t, tc.expected, result)
	}
}

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
