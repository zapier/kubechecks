package affected_apps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zapier/kubechecks/pkg/config"
)

func TestBestEffortMatcher(t *testing.T) {
	type args struct {
		fileList []string
		repoName string
	}
	tests := []struct {
		name string
		args args
		want AffectedItems
	}{
		{
			name: "helm:cluster-change",
			args: args{
				fileList: []string{
					"apps/echo-server/foo-eks-01/values.yaml",
				},
				repoName: "",
			},
			want: AffectedItems{
				Applications: []config.ApplicationStub{
					{Name: "foo-eks-01-echo-server", Path: "apps/echo-server/foo-eks-01/"},
				},
			},
		},
		{
			name: "helm:all-cluster-change",
			args: args{
				fileList: []string{
					"apps/echo-server/values.yaml",
				},
				repoName: "",
			},
			want: AffectedItems{
				Applications: []config.ApplicationStub{
					{Name: "foo-eks-01-echo-server", Path: "apps/echo-server/foo-eks-01/"},
					{Name: "foo-eks-02-echo-server", Path: "apps/echo-server/foo-eks-02/"},
				},
			},
		},
		{
			name: "helm:all-cluster-change:and:cluster-app-change",
			args: args{
				fileList: []string{
					"apps/echo-server/values.yaml",
					"apps/echo-server/foo-eks-01/values.yaml",
				},
				repoName: "",
			},
			want: AffectedItems{
				Applications: []config.ApplicationStub{
					{Name: "foo-eks-01-echo-server", Path: "apps/echo-server/foo-eks-01/"},
					{Name: "foo-eks-02-echo-server", Path: "apps/echo-server/foo-eks-02/"},
				},
			},
		},
		{
			name: "helm:all-cluster-change:and:double-cluster-app-change",
			args: args{
				fileList: []string{
					"apps/echo-server/values.yaml",
					"apps/echo-server/foo-eks-01/values.yaml",
					"apps/echo-server/foo-eks-02/values.yaml",
				},
				repoName: "",
			},
			want: AffectedItems{
				Applications: []config.ApplicationStub{
					{Name: "foo-eks-01-echo-server", Path: "apps/echo-server/foo-eks-01/"},
					{Name: "foo-eks-02-echo-server", Path: "apps/echo-server/foo-eks-02/"},
				},
			},
		},
		{
			name: "kustomize:overlays-change",
			args: args{
				fileList: []string{
					"apps/httpbin/overlays/foo-eks-01/kustomization.yaml",
				},
				repoName: "",
			},
			want: AffectedItems{
				Applications: []config.ApplicationStub{
					{Name: "foo-eks-01-httpbin", Path: "apps/httpbin/overlays/foo-eks-01/"},
				},
			},
		},
		{
			name: "kustomize:overlays-subdir-change",
			args: args{
				fileList: []string{
					"apps/httpbin/overlays/foo-eks-01/server/deploy.yaml",
				},
				repoName: "",
			},
			want: AffectedItems{
				Applications: []config.ApplicationStub{
					{Name: "foo-eks-01-httpbin", Path: "apps/httpbin/overlays/foo-eks-01/"},
				},
			},
		},
		{
			name: "kustomize:base-change",
			args: args{
				fileList: []string{
					"apps/httpbin/base/kustomization.yaml",
				},
				repoName: "",
			},
			want: AffectedItems{
				Applications: []config.ApplicationStub{
					{Name: "foo-eks-01-httpbin", Path: "apps/httpbin/overlays/foo-eks-01/"},
				},
			},
		},
		{
			name: "kustomize:bases-change",
			args: args{
				fileList: []string{
					"apps/httpbin/bases/foo.yaml",
				},
				repoName: "",
			},
			want: AffectedItems{
				Applications: []config.ApplicationStub{
					{Name: "foo-eks-01-httpbin", Path: "apps/httpbin/overlays/foo-eks-01/"},
				},
			},
		},
		{
			name: "kustomize:resources-change",
			args: args{
				fileList: []string{
					"apps/httpbin/resources/foo.yaml",
				},
				repoName: "",
			},
			want: AffectedItems{
				Applications: []config.ApplicationStub{
					{Name: "foo-eks-01-httpbin", Path: "apps/httpbin/overlays/foo-eks-01/"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got AffectedItems
			var err error

			matcher := NewBestEffortMatcher(tt.args.repoName, testRepoFiles)
			got, err = matcher.AffectedApps(context.TODO(), tt.args.fileList)
			require.NoError(t, err)

			assert.Equal(t, len(tt.want.Applications), len(got.Applications))
			assert.Equal(t, len(tt.want.ApplicationSets), len(got.ApplicationSets))

			// ordering doesn't matter, we just want to make sure the items all exist
			wantAppsMap := listToMap(tt.want.Applications, appStubKey)
			gotAppsMap := listToMap(got.Applications, appStubKey)
			assert.Equal(t, wantAppsMap, gotAppsMap, "Applications not equal")

			wantAppSetsMap := listToMap(tt.want.ApplicationSets, appSetKey)
			gotAppSetsMap := listToMap(got.ApplicationSets, appSetKey)
			assert.Equal(t, wantAppSetsMap, gotAppSetsMap, "ApplicationSets not equal")
		})
	}
}

func appSetKey(item ApplicationSet) string {
	return item.Name
}

func appStubKey(stub config.ApplicationStub) string {
	return stub.Name
}

func listToMap[T any](items []T, makeKey func(T) string) map[string]T {
	result := make(map[string]T)
	for _, item := range items {
		key := makeKey(item)
		result[key] = item
	}
	return result
}

var testRepoFiles = []string{
	"apps/echo-server/foo-eks-01/Chart.yaml",
	"apps/echo-server/foo-eks-01/values.yaml",
	"apps/echo-server/foo-eks-01/templates/something.yaml",
	"apps/echo-server/foo-eks-02/Chart.yaml",
	"apps/echo-server/foo-eks-02/values.yaml",
	"apps/echo-server/foo-eks-02/templates/something.yaml",
	"apps/echo-server/values.yaml",
	"apps/echo-server/opslevel.yml",
	"apps/httpbin/base/kustomization.yaml",
	"apps/httpbin/bases/deploy.yaml",
	"apps/httpbin/resources/configmap.yaml",
	"apps/httpbin/overlays/foo-eks-01/kustomization.yaml",
	"apps/httpbin/overlays/foo-eks-01/server/deploy.yaml",
	"apps/httpbin/components/kustomization.yaml",
}

func Test_isKustomizeApp(t *testing.T) {
	type args struct {
		file string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"overlayskustomzation.yaml",
			args{
				"apps/foo/overlays/kustomization.yaml",
			},
			true,
		},
		{
			"basekustomzation.yaml",
			args{
				"apps/foo/overlays/kustomization.yaml",
			},
			true,
		},
		{
			"overlaysfile",
			args{
				"apps/foo/overlays/foo.yaml",
			},
			true,
		},
		{
			"basefile",
			args{
				"apps/foo/base/foo.yaml",
			},
			true,
		},
		{
			"helmvalues",
			args{
				"apps/foo/values.yaml",
			},
			false,
		},
		{
			"helmclustervalues",
			args{
				"apps/foo/cluster/values.yaml",
			},
			false,
		},
		{
			"helmvalues",
			args{
				"apps/foo/values.yaml",
			},
			false,
		},
		{
			"basesfile",
			args{
				"apps/foo/bases/foo.yaml",
			},
			true,
		},
		{
			"resourcesfile",
			args{
				"apps/foo/resources/foo.yaml",
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKustomizeApp(tt.args.file); got != tt.want {
				t.Errorf("isKustomizeApp() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_overlaysDir(t *testing.T) {
	type args struct {
		file string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			"basic",
			args{
				file: "apps/foo/base/kustomization.yaml",
			},
			"apps/foo/overlays/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := overlaysDir(tt.args.file); got != tt.want {
				t.Errorf("overlaysDir() = %v, want %v", got, tt.want)
			}
		})
	}
}
