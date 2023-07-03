package affected_apps

import (
	"context"
	"io"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/repo_config"
)

type MockArgoApplicationServiceClient struct {
	application.ApplicationServiceClient
}

type MockCloser struct {
	CloseFunc func() error
}

func (m MockCloser) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

type MockArgoClient struct {
	apiclient.Client
}

func (m MockArgoApplicationServiceClient) List(_ context.Context, _ *application.ApplicationQuery, _ ...grpc.CallOption) (*v1alpha1.ApplicationList, error) {
	return &v1alpha1.ApplicationList{}, nil
}

func (m MockArgoClient) NewApplicationClient() (io.Closer, application.ApplicationServiceClient, error) {
	return MockCloser{}, MockArgoApplicationServiceClient{}, nil

}

func NewMockArgoClient() *argo_client.ArgoClient {
	apiClient := MockArgoClient{}
	return argo_client.NewArgoClient(apiClient)
}

func Test_dirMatchForApp(t *testing.T) {
	type args struct {
		changeDir string
		appDir    string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"/tmp/repo/apps/appdir/",
			args{
				"/tmp/repo/apps/appdir/",
				"apps/appdir",
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dirMatchForApp(tt.args.changeDir, tt.args.appDir); got != tt.want {
				t.Errorf("dirMatchForApp() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigMatcher_triggeredApps(t *testing.T) {

	type args struct {
		modifiedFiles []string
	}
	tests := []struct {
		name      string
		configDir string
		args      args
	}{
		{
			"match-app-dir",
			"../repo_config/testdata/1/",
			args{
				[]string{
					"k8s/prod-k8s-01/values.yaml",
					"apps/httpdump/base/kustomization.yaml",
				},
			},
		},
		{
			"match-additional-dir",
			"../repo_config/testdata/2/",
			args{
				[]string{
					"k8s/env/prod//values.yaml",
					"apps//httpdump/overlays/in-cluster/kustomization.yaml",
				},
			},
		},
		{
			"multiple-matches",
			"../repo_config/testdata/3/",
			args{
				[]string{
					"k8s/prod-k8s-01/values.yaml",
					"k8s/prod-k8s-02/values.yaml",
					"apps/httpdump/base/kustomization.yaml",
					"apps/echo-server/values.yaml",
				},
			},
		},
		{
			"multiple-match-additional-dir",
			"../repo_config/testdata/3/",
			args{
				[]string{
					"k8s/env/prod/values.yaml",
					"apps/echo-server/in-cluster/values.yaml",
					"apps//httpdump/overlays/in-cluster/kustomization.yaml",
					"apps/httpdump/base/kustomization.yaml",
				},
			},
		},
	}

	mockArgoClient := NewMockArgoClient()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testLoadConfig(t, tt.configDir)
			b := &ConfigMatcher{
				cfg:        c,
				argoClient: mockArgoClient,
			}
			gotApps, gotAppSets, _ := b.triggeredApps(context.TODO(), tt.args.modifiedFiles)
			assert.ElementsMatch(t, gotApps, c.Applications, "applications did not match.")
			assert.ElementsMatch(t, gotAppSets, c.ApplicationSets, "applicationsets did not match.")
		})
	}
}

func testLoadConfig(t *testing.T, configDir string) *repo_config.Config {
	cfg, err := repo_config.LoadRepoConfig(configDir)
	if err != nil {
		t.Errorf("could not load test config from dir (%s): %v", configDir, err)
	}
	return cfg
}

func TestDirMatchForAppSet(t *testing.T) {
	type args struct {
		changeDir string
		appSetDir string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"/tmp/repo/apps/appdir/",
			args{
				"/tmp/repo/apps/appdir/",
				"/tmp/repo/apps/appdir",
			},
			true,
		},
		{
			"/tmp/repo/apps/appdir/",
			args{
				"/tmp/repo/apps/appdir",
				"/tmp/repo/apps/",
			},
			true,
		},
		{
			"/tmp/repo/apps/appdir/",
			args{
				"/tmp/repo/apps/appsetdir",
				"apps/appdir",
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dirMatchForAppSet(tt.args.changeDir, tt.args.appSetDir); got != tt.want {
				t.Errorf("dirMatchForAppSet() = %v, want %v", got, tt.want)
			}
		})
	}
}
