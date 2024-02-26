package cmd

import (
	"context"
	"fmt"

	"github.com/pkg/errors"

	"github.com/zapier/kubechecks/pkg/app_watcher"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs/github_client"
	"github.com/zapier/kubechecks/pkg/vcs/gitlab_client"
)

func newContainer(ctx context.Context, cfg config.ServerConfig) (container.Container, error) {
	var err error

	var ctr = container.Container{
		Config: cfg,
	}

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

	if ctr.ArgoClient, err = argo_client.NewArgoClient(cfg); err != nil {
		return ctr, errors.Wrap(err, "failed to create argo client")
	}

	vcsToArgoMap := appdir.NewVcsToArgoMap()
	ctr.VcsToArgoMap = vcsToArgoMap

	if cfg.MonitorAllApplications {
		if err = buildAppsMap(ctx, ctr.ArgoClient, ctr.VcsToArgoMap); err != nil {
			return ctr, errors.Wrap(err, "failed to build apps map")
		}

		ctr.ApplicationWatcher, err = app_watcher.NewApplicationWatcher(vcsToArgoMap)
		if err != nil {
			return ctr, errors.Wrap(err, "failed to create watch applications")
		}

		go ctr.ApplicationWatcher.Run(ctx, 1)
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
