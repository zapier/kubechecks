package container

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	client "github.com/zapier/kubechecks/pkg/kubernetes"
	"github.com/zapier/kubechecks/pkg/vcs/github_client"
	"github.com/zapier/kubechecks/pkg/vcs/gitlab_client"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/vcs"
)

var tracer = otel.Tracer("pkg/container")

type Container struct {
	ArgoClient *argo_client.ArgoClient

	Config config.ServerConfig

	RepoManager git.RepoManager

	VcsClient    vcs.Client
	VcsToArgoMap appdir.VcsToArgoMap

	KubeClientSet client.Interface
}

type ReposCache interface {
	Clone(ctx context.Context, repoUrl string) (string, error)
	CloneWithBranch(ctx context.Context, repoUrl, targetBranch string) (string, error)
}

func New(ctx context.Context, cfg config.ServerConfig) (Container, error) {
	ctx, span := tracer.Start(ctx, "New")
	defer span.End()

	var err error

	var ctr = Container{
		Config:      cfg,
		RepoManager: git.NewRepoManager(cfg),
	}

	// create vcs client
	switch cfg.VcsType {
	case "gitlab":
		ctr.VcsClient, err = gitlab_client.CreateGitlabClient(ctx, cfg)
	case "github":
		ctr.VcsClient, err = github_client.CreateGithubClient(ctx, cfg)
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
	if ctr.ArgoClient, err = argo_client.NewArgoClient(cfg, kubeClient); err != nil {
		return ctr, errors.Wrap(err, "failed to create argo client")
	}

	// create vcs to argo map
	vcsToArgoMap := appdir.NewVcsToArgoMap(ctr.VcsClient.Username())
	ctr.VcsToArgoMap = vcsToArgoMap

	if cfg.MonitorAllApplications {
		if err = buildAppsMap(ctx, ctr.ArgoClient, ctr.VcsToArgoMap); err != nil {
			log.Fatal().Err(err).Msg("failed to build apps map")
		}

		if err = buildAppSetsMap(ctx, ctr.ArgoClient, ctr.VcsToArgoMap); err != nil {
			log.Fatal().Err(err).Msg("failed to build appsets map")
		}
	}

	return ctr, nil
}

func buildAppsMap(ctx context.Context, argoClient *argo_client.ArgoClient, result appdir.VcsToArgoMap) error {
	apps, err := argoClient.GetApplications(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list applications")
	}
	for _, app := range apps.Items {
		result.AddApp(&app)
	}
	return nil
}

func buildAppSetsMap(ctx context.Context, argoClient *argo_client.ArgoClient, result appdir.VcsToArgoMap) error {
	appSets, err := argoClient.GetApplicationSets(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list application sets")
	}
	for _, appSet := range appSets.Items {
		result.AddAppSet(&appSet)
	}
	return nil
}

// Shutdown gracefully shuts down the container and its components
func (c *Container) Shutdown() {
	if c.RepoManager != nil {
		c.RepoManager.Shutdown()
	}
}
