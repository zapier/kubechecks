package repo_config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_loadProjectConfigFile(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not determine cwd for test: %v", err)
	}
	type args struct {
		file string
	}
	tests := []struct {
		name    string
		args    args
		want    *Config
		wantErr bool
	}{
		{
			name: "yaml",
			args: args{
				file: cwd + "/testdata/1/.kubechecks.yaml",
			},
			want: &Config{
				Applications: []*ArgoCdApplicationConfig{
					defaultArgoCdApplicationConfig().
						withCluster("prod-k8s-01").
						withName("prod-k8s-01-httpbin").
						withPath("k8s/prod-k8s-01/"),
				},
				ApplicationSets: []*ArgocdApplicationSetConfig{
					defaultArgoCdApplicationSetConfig().
						withName("httpdump").
						withPaths("apps/httpdump/base"),
				},
			},
			wantErr: false,
		},
		{
			name: "yml",
			args: args{
				file: cwd + "/testdata/2/.kubechecks.yml",
			},
			want: &Config{
				Applications: []*ArgoCdApplicationConfig{
					defaultArgoCdApplicationConfig().
						withCluster("prod-k8s-01").
						withName("prod-k8s-01-httpbin").
						withPath("k8s/prod-k8s-01/").
						withAdditionalPaths("k8s/env/prod/"),
				},
				ApplicationSets: []*ArgocdApplicationSetConfig{
					defaultArgoCdApplicationSetConfig().
						withName("httpdump").
						withPaths("apps/httpdump/base", "apps/httpdump/overlays/in-cluster"),
				},
			},
			wantErr: false,
		},
		{
			name: "multiple-apps",
			args: args{
				file: cwd + "/testdata/3/.kubechecks.yaml",
			},
			want: &Config{
				Applications: []*ArgoCdApplicationConfig{
					defaultArgoCdApplicationConfig().
						withCluster("prod-k8s-01").
						withName("prod-k8s-01-httpbin").
						withPath("k8s/prod-k8s-01/").
						withAdditionalPaths("k8s/env/prod/"),

					defaultArgoCdApplicationConfig().
						withCluster("prod-k8s-02").
						withName("prod-k8s-02-httpbin").
						withPath("k8s/prod-k8s-02/").
						withAdditionalPaths("k8s/env/prod/"),
				},
				ApplicationSets: []*ArgocdApplicationSetConfig{
					defaultArgoCdApplicationSetConfig().
						withName("httpdump").
						withPaths("apps/httpdump/base", "apps/httpdump/overlays/in-cluster"),
					defaultArgoCdApplicationSetConfig().
						withName("echo-server").
						withPaths("apps/echo-server", "apps/echo-server/in-cluster"),
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadRepoConfigFile(tt.args.file)
			if (err != nil) != tt.wantErr {
				t.Errorf("loadRepoConfigFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			assert.Equal(t, got, tt.want, "Configs are not the same.")
		})
	}
}

func Test_searchConfigFile(t *testing.T) {
	type args struct {
		repoDir string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name:    "1",
			args:    args{repoDir: "testdata/1"},
			want:    "testdata/1/.kubechecks.yaml",
			wantErr: false,
		},
		{
			name:    "2",
			args:    args{repoDir: "testdata/2"},
			want:    "testdata/2/.kubechecks.yml",
			wantErr: false,
		},
		{
			name:    "not-exist",
			args:    args{repoDir: "testdata/notexist"},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := searchConfigFile(tt.args.repoDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("searchConfigFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("searchConfigFile() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadRepoConfig(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not determine cwd for test: %v", err)
	}
	type args struct {
		repoDir string
	}
	tests := []struct {
		name    string
		args    args
		want    *Config
		wantErr bool
	}{

		{
			name: "yaml",
			args: args{
				repoDir: cwd + "/testdata/1/",
			},
			want: &Config{
				Applications: []*ArgoCdApplicationConfig{
					defaultArgoCdApplicationConfig().
						withCluster("prod-k8s-01").
						withName("prod-k8s-01-httpbin").
						withPath("k8s/prod-k8s-01/"),
				},
				ApplicationSets: []*ArgocdApplicationSetConfig{
					defaultArgoCdApplicationSetConfig().
						withName("httpdump").
						withPaths("apps/httpdump/base"),
				},
			},
			wantErr: false,
		},
		{
			name: "yml",
			args: args{
				repoDir: cwd + "/testdata/2/",
			},
			want: &Config{
				Applications: []*ArgoCdApplicationConfig{
					defaultArgoCdApplicationConfig().
						withCluster("prod-k8s-01").
						withName("prod-k8s-01-httpbin").
						withPath("k8s/prod-k8s-01/").
						withAdditionalPaths("k8s/env/prod/"),
				},
				ApplicationSets: []*ArgocdApplicationSetConfig{
					defaultArgoCdApplicationSetConfig().
						withName("httpdump").
						withPaths("apps/httpdump/base", "apps/httpdump/overlays/in-cluster"),
				},
			},
			wantErr: false,
		},
		{
			name: "multiple-apps",
			args: args{
				repoDir: cwd + "/testdata/3/",
			},
			want: &Config{
				Applications: []*ArgoCdApplicationConfig{
					defaultArgoCdApplicationConfig().
						withCluster("prod-k8s-01").
						withName("prod-k8s-01-httpbin").
						withPath("k8s/prod-k8s-01/").
						withAdditionalPaths("k8s/env/prod/"),

					defaultArgoCdApplicationConfig().
						withCluster("prod-k8s-02").
						withName("prod-k8s-02-httpbin").
						withPath("k8s/prod-k8s-02/").
						withAdditionalPaths("k8s/env/prod/"),
				},
				ApplicationSets: []*ArgocdApplicationSetConfig{
					defaultArgoCdApplicationSetConfig().
						withName("httpdump").
						withPaths("apps/httpdump/base", "apps/httpdump/overlays/in-cluster"),
					defaultArgoCdApplicationSetConfig().
						withName("echo-server").
						withPaths("apps/echo-server", "apps/echo-server/in-cluster"),
				},
			},
			wantErr: false,
		},
		{
			name: "not-found",
			args: args{
				repoDir: cwd + "/testdata/not-found/",
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadRepoConfig(tt.args.repoDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadRepoConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, got, tt.want, "Configs are not the same.")
		})
	}
}
