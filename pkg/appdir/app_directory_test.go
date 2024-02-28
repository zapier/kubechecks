package appdir

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPathsAreJoinedProperly(t *testing.T) {
	rad := NewAppDirectory()
	app1 := v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-app",
		},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{
				Path: "/test1/test2",
				Helm: &v1alpha1.ApplicationSourceHelm{
					ValueFiles: []string{"one.yaml", "./two.yaml", "../three.yaml"},
					FileParameters: []v1alpha1.HelmFileParameter{
						{Name: "one", Path: "one.json"},
						{Name: "two", Path: "./two.json"},
						{Name: "three", Path: "../three.json"},
					},
				},
			},
		},
	}

	rad.ProcessApp(app1)

	assert.Equal(t, map[string]v1alpha1.Application{
		"test-app": app1,
	}, rad.appsMap)
	assert.Equal(t, map[string][]string{
		"/test1/test2": {"test-app"},
	}, rad.appDirs)
	assert.Equal(t, map[string][]string{
		"/test1/test2/one.json": {"test-app"},
		"/test1/test2/two.json": {"test-app"},
		"/test1/three.json":     {"test-app"},
		"/test1/test2/one.yaml": {"test-app"},
		"/test1/test2/two.yaml": {"test-app"},
		"/test1/three.yaml":     {"test-app"},
	}, rad.appFiles)
}

func TestShouldInclude(t *testing.T) {
	testcases := []struct {
		vcsMergeTarget  string
		argocdAppBranch string
		expected        bool
	}{
		{
			vcsMergeTarget:  "some-branch",
			argocdAppBranch: "some-branch",
			expected:        true,
		},
		{
			vcsMergeTarget:  "some-branch",
			argocdAppBranch: "some-other-branch",
			expected:        false,
		},
		{
			argocdAppBranch: "HEAD",
			vcsMergeTarget:  "main",
			expected:        true,
		},
		{
			argocdAppBranch: "HEAD",
			vcsMergeTarget:  "master",
			expected:        true,
		},
		{
			argocdAppBranch: "HEAD",
			vcsMergeTarget:  "other",
			expected:        false,
		},
		{
			argocdAppBranch: "",
			vcsMergeTarget:  "branch",
			expected:        true,
		},
	}

	for _, tc := range testcases {
		t.Run(fmt.Sprintf("%v", tc), func(t *testing.T) {
			actual := shouldInclude(v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						TargetRevision: tc.argocdAppBranch,
					},
				},
			}, tc.vcsMergeTarget)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

// TestRemoveFromSlice performs tests on the removeFromSlice function.
func TestRemoveFromSlice(t *testing.T) {
	// Test for integers
	ints := []int{1, 2, 3, 4, 5}
	intsAfterRemoval := []int{1, 2, 4, 5}
	intsTest := func(t *testing.T) {
		result := removeFromSlice(ints, 3, func(a, b int) bool { return a == b })
		if !reflect.DeepEqual(result, intsAfterRemoval) {
			t.Errorf("Expected %v, got %v", intsAfterRemoval, result)
		}
	}

	// Test for strings
	strings := []string{"apple", "banana", "cherry", "date"}
	stringsAfterRemoval := []string{"apple", "cherry", "date"}
	stringsTest := func(t *testing.T) {
		result := removeFromSlice(strings, "banana", func(a, b string) bool { return a == b })
		if !reflect.DeepEqual(result, stringsAfterRemoval) {
			t.Errorf("Expected %v, got %v", stringsAfterRemoval, result)
		}
	}

	// Execute subtests
	t.Run("Integers", intsTest)
	t.Run("Strings", stringsTest)
	// Add more subtests for different generic types if necessary
}
