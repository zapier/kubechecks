package argo_client

import (
	"context"
	"fmt"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient/cluster"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/settings"
	argoappv1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	repoapiclient "github.com/argoproj/argo-cd/v2/reposerver/apiclient"
	"github.com/argoproj/argo-cd/v2/reposerver/repository"
	"github.com/argoproj/argo-cd/v2/util/git"
	"github.com/argoproj/argo-cd/v2/util/manifeststream"
	"github.com/ghodss/yaml"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/zapier/kubechecks/telemetry"
)

func (argo *ArgoClient) GetManifestsLocal(ctx context.Context, name, tempRepoDir, changedAppFilePath string, app argoappv1.Application) ([]string, error) {
	var err error

	ctx, span := tracer.Start(ctx, "GetManifestsLocal")
	defer span.End()

	log.Debug().Str("name", name).Msg("GetManifestsLocal")

	start := time.Now()
	defer func() {
		duration := time.Since(start)
		getManifestsDuration.WithLabelValues(name).Observe(duration.Seconds())
	}()

	clusterCloser, clusterClient := argo.GetClusterClient()
	defer clusterCloser.Close()

	settingsCloser, settingsClient := argo.GetSettingsClient()
	defer settingsCloser.Close()

	log.Debug().
		Str("clusterName", app.Spec.Destination.Name).
		Str("clusterServer", app.Spec.Destination.Server).
		Msg("getting cluster")
	cluster, err := clusterClient.Get(ctx, &cluster.ClusterQuery{Name: app.Spec.Destination.Name, Server: app.Spec.Destination.Server})
	if err != nil {
		telemetry.SetError(span, err, "Argo Get Cluster")
		getManifestsFailed.WithLabelValues(name).Inc()
		return nil, errors.Wrap(err, "failed to get cluster")
	}

	argoSettings, err := settingsClient.Get(ctx, &settings.SettingsQuery{})
	if err != nil {
		telemetry.SetError(span, err, "Argo Get Settings")
		getManifestsFailed.WithLabelValues(name).Inc()
		return nil, errors.Wrap(err, "failed to get settings")
	}

	log.Debug().Str("name", name).Msg("generating diff for application...")
	res, err := argo.generateManifests(ctx, fmt.Sprintf("%s/%s", tempRepoDir, changedAppFilePath), tempRepoDir, app, argoSettings, cluster)
	if err != nil {
		telemetry.SetError(span, err, "Generate Manifests")
		return nil, errors.Wrap(err, "failed to generate manifests")
	}

	if res.Manifests == nil {
		return nil, nil
	}
	getManifestsSuccess.WithLabelValues(name).Inc()
	return res.Manifests, nil
}

func (argo *ArgoClient) generateManifests(
	ctx context.Context, appPath, tempRepoDir string, app argoappv1.Application, argoSettings *settings.Settings, cluster *argoappv1.Cluster,
) (*repoapiclient.ManifestResponse, error) {
	argo.manifestsLock.Lock()
	defer argo.manifestsLock.Unlock()

	source := app.Spec.GetSource()

	return repository.GenerateManifests(
		ctx,
		appPath,
		tempRepoDir,
		source.TargetRevision,
		&repoapiclient.ManifestRequest{
			Repo:              &argoappv1.Repository{Repo: source.RepoURL},
			AppLabelKey:       argoSettings.AppLabelKey,
			AppName:           app.Name,
			Namespace:         app.Spec.Destination.Namespace,
			ApplicationSource: &source,
			KustomizeOptions:  argoSettings.KustomizeOptions,
			KubeVersion:       cluster.Info.ServerVersion,
			ApiVersions:       cluster.Info.APIVersions,
			TrackingMethod:    argoSettings.TrackingMethod,
		},
		true,
		new(git.NoopCredsStore),
		resource.MustParse("0"),
		nil,
	)
}

// adapted fromm https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L894
func (argo *ArgoClient) GetManifestsServerSide(ctx context.Context, name, tempRepoDir, changedAppFilePath string, app argoappv1.Application) ([]string, error) {
	var err error

	ctx, span := tracer.Start(ctx, "GetManifestsServerSide")
	defer span.End()

	log.Debug().Str("name", name).Msg("GetManifestsServerSide")

	start := time.Now()
	defer func() {
		duration := time.Since(start)
		getManifestsDuration.WithLabelValues(name).Observe(duration.Seconds())
	}()

	appCloser, appClient := argo.GetApplicationClient()
	defer appCloser.Close()

	client, err := appClient.GetManifestsWithFiles(ctx, grpc_retry.Disable())
	if err != nil {
		return nil, err
	}
	localIncludes := []string{"*"}
	log.Debug().Str("name", name).Str("repo_path", tempRepoDir).Msg("sending application manifest query with files")

	err = manifeststream.SendApplicationManifestQueryWithFiles(ctx, client, name, app.Namespace, tempRepoDir, localIncludes)
	if err != nil {
		return nil, err
	}

	res, err := client.CloseAndRecv()
	if err != nil {
		return nil, err
	}

	if res.Manifests == nil {
		return nil, nil
	}
	getManifestsSuccess.WithLabelValues(name).Inc()
	return res.Manifests, nil
}

func ConvertJsonToYamlManifests(jsonManifests []string) []string {
	var manifests []string
	for _, manifest := range jsonManifests {
		ret, err := yaml.JSONToYAML([]byte(manifest))
		if err != nil {
			log.Warn().Err(err).Msg("Failed to format manifest")
			continue
		}
		manifests = append(manifests, fmt.Sprintf("---\n%s", string(ret)))
	}
	return manifests
}
