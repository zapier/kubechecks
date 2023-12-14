package config

import (
	"fmt"
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

	assert.Equal(t, map[string]ApplicationStub{
		"test-app": {
			Name: "test-app",
			Path: "/test1/test2",
		},
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
			actual := shouldInclude(ApplicationStub{TargetRevision: tc.argocdAppBranch}, tc.vcsMergeTarget)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
