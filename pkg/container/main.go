package container

import (
	"context"
	"io/fs"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	client "github.com/zapier/kubechecks/pkg/kubernetes"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/app_watcher"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/vcs"
)

type Container struct {
	ApplicationWatcher    *app_watcher.ApplicationWatcher
	ApplicationSetWatcher *app_watcher.ApplicationSetWatcher
	ArgoClient            *argo_client.ArgoClient

	Config config.ServerConfig

	RepoManager *git.RepoManager

	VcsClient    vcs.Client
	VcsToArgoMap VcsToArgoMap

	KubeClientSet client.Interface
}

type VcsToArgoMap interface {
	AddApp(*v1alpha1.Application)
	AddAppSet(*v1alpha1.ApplicationSet)
	UpdateApp(old, new *v1alpha1.Application)
	UpdateAppSet(old *v1alpha1.ApplicationSet, new *v1alpha1.ApplicationSet)
	DeleteApp(*v1alpha1.Application)
	DeleteAppSet(app *v1alpha1.ApplicationSet)
	GetVcsRepos() []string
	GetAppsInRepo(string) *appdir.AppDirectory
	GetAppSetsInRepo(string) *appdir.AppSetDirectory
	GetMap() map[pkg.RepoURL]*appdir.AppDirectory
	WalkKustomizeApps(cloneURL string, fs fs.FS) *appdir.AppDirectory
}

type ReposCache interface {
	Clone(ctx context.Context, repoUrl string) (string, error)
	CloneWithBranch(ctx context.Context, repoUrl, targetBranch string) (string, error)
}
