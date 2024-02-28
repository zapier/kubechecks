package container

import (
	"context"
	"io/fs"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"

	"github.com/zapier/kubechecks/pkg/app_watcher"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

type Container struct {
	ApplicationWatcher *app_watcher.ApplicationWatcher
	ArgoClient         *argo_client.ArgoClient

	Config config.ServerConfig

	VcsClient    vcs.VcsClient
	VcsToArgoMap VcsToArgoMap
	ReposCache   ReposCache
}

type VcsToArgoMap interface {
	AddApp(*v1alpha1.Application)
	UpdateApp(old, new *v1alpha1.Application)
	DeleteApp(*v1alpha1.Application)
	GetVcsRepos() []string
	GetAppsInRepo(string) *appdir.AppDirectory
	GetMap() map[appdir.RepoURL]*appdir.AppDirectory
	WalkKustomizeApps(cloneURL string, fs fs.FS) *appdir.AppDirectory
}

type ReposCache interface {
	Clone(ctx context.Context, repoUrl string) (string, error)
	CloneWithBranch(ctx context.Context, repoUrl, targetBranch string) (string, error)
}
