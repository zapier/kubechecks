package cmd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/pkg/errors"
	"github.com/zapier/kubechecks/pkg/app_watcher"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
	client "github.com/zapier/kubechecks/pkg/kubernetes"
	"github.com/zapier/kubechecks/pkg/vcs/github_client"
	"github.com/zapier/kubechecks/pkg/vcs/gitlab_client"
)

func newContainer(ctx context.Context, cfg config.ServerConfig, watchApps bool) (container.Container, error) {
	var err error

	var ctr = container.Container{
		Config:      cfg,
		RepoManager: git.NewRepoManager(cfg),
	}

	// create vcs client
	switch cfg.VcsType {
	case "gitlab":
		ctr.VcsClient, err = gitlab_client.CreateGitlabClient(cfg)
	case "github":
		ctr.VcsClient, err = github_client.CreateGithubClient(cfg)
	default:
		err = fmt.Errorf("unknown vcs-type: %q", cfg.VcsType)
	}
	if err != nil {
		return ctr, errors.Wrap(err, "failed to create vcs client")
	}
	var kubeClient client.Interface

	switch cfg.KubernetesType {
	// TODO: expand with other cluster types
	case client.ClusterTypeLOCAL:
		kubeClient, err = client.New(&client.NewClientInput{
			KubernetesConfigPath: cfg.KubernetesConfig,
			ClusterType:          cfg.KubernetesType,
		})
		if err != nil {
			return ctr, errors.Wrap(err, "failed to create kube client")
		}
	case client.ClusterTypeEKS:
		kubeClient, err = client.New(&client.NewClientInput{
			KubernetesConfigPath: cfg.KubernetesConfig,
			ClusterType:          cfg.KubernetesType,
		},
			client.EKSClientOption(ctx, cfg.KubernetesClusterID),
		)
		if err != nil {
			return ctr, errors.Wrap(err, "failed to create kube client")
		}
	}
	ctr.KubeClientSet = kubeClient
	// create argo client
	if ctr.ArgoClient, err = argo_client.NewArgoClient(cfg); err != nil {
		return ctr, errors.Wrap(err, "failed to create argo client")
	}

	// create vcs to argo map
	vcsToArgoMap := appdir.NewVcsToArgoMap(ctr.VcsClient.Username())
	ctr.VcsToArgoMap = vcsToArgoMap

	// watch app modifications, if necessary
	if cfg.MonitorAllApplications {
		if err = buildAppsMap(ctx, ctr.ArgoClient, ctr.VcsToArgoMap); err != nil {
			return ctr, errors.Wrap(err, "failed to build apps map")
		}

		if err = buildAppSetsMap(ctx, ctr.ArgoClient, ctr.VcsToArgoMap); err != nil {
			return ctr, errors.Wrap(err, "failed to build appsets map")
		}

		if watchApps {
			ctr.ApplicationWatcher, err = app_watcher.NewApplicationWatcher(kubeClient.Config(), vcsToArgoMap, cfg)
			if err != nil {
				return ctr, errors.Wrap(err, "failed to create watch applications")
			}
			ctr.ApplicationSetWatcher, err = app_watcher.NewApplicationSetWatcher(kubeClient.Config(), vcsToArgoMap, cfg)
			if err != nil {
				return ctr, errors.Wrap(err, "failed to create watch application sets")
			}

			go ctr.ApplicationWatcher.Run(ctx, 1)
			go ctr.ApplicationSetWatcher.Run(ctx)
		}
	} else {
		slog.Info(fmt.Sprintf("not monitoring applications, MonitorAllApplications: %+v", cfg.MonitorAllApplications))
	}

	return ctr, nil
}

func buildAppsMap(ctx context.Context, argoClient *argo_client.ArgoClient, result container.VcsToArgoMap) error {
	apps, err := argoClient.GetApplications(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list applications")
	}
	for _, app := range apps.Items {
		result.AddApp(&app)
	}
	return nil
}

func buildAppSetsMap(ctx context.Context, argoClient *argo_client.ArgoClient, result container.VcsToArgoMap) error {
	appSets, err := argoClient.GetApplicationSets(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list application sets")
	}
	for _, appSet := range appSets.Items {
		result.AddAppSet(&appSet)
	}
	return nil
}
