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
	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/zapier/kubechecks/telemetry"
)

func GetManifestsLocal(ctx context.Context, argoClient *ArgoClient, name string, tempRepoDir string, changedAppFilePath string, app argoappv1.Application) ([]string, error) {
	var err error

	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "GetManifestsLocal")
	defer span.End()

	log.Debug().Str("name", name).Msg("GetManifestsLocal")

	start := time.Now()
	defer func() {
		duration := time.Since(start)
		getManifestsDuration.WithLabelValues(name).Observe(duration.Seconds())
	}()

	clusterCloser, clusterClient := argoClient.GetClusterClient()
	defer clusterCloser.Close()

	settingsCloser, settingsClient := argoClient.GetSettingsClient()
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

	// Code is commented out until Argo fixes the server side manifest generation
	/*
		localIncludes := []string{"*.yaml", "*.json", "*.yml"}
		// sends files to argocd to generate a diff based on them.

		client, err := appClient.GetManifestsWithFiles(context.Background(), grpc_retry.Disable())
		errors.CheckError(err)

		err = manifeststream.SendApplicationManifestQueryWithFiles(context.Background(), client, appName, appNamespace, changedFilePath, localIncludes)
		errors.CheckError(err)
	*/

	source := app.Spec.GetSource()

	log.Debug().Str("name", name).Msg("generating diff for application...")
	res, err := repository.GenerateManifests(ctx, fmt.Sprintf("%s/%s", tempRepoDir, changedAppFilePath), tempRepoDir, source.TargetRevision, &repoapiclient.ManifestRequest{
		Repo:              &argoappv1.Repository{Repo: source.RepoURL},
		AppLabelKey:       argoSettings.AppLabelKey,
		AppName:           app.Name,
		Namespace:         app.Spec.Destination.Namespace,
		ApplicationSource: &source,
		KustomizeOptions:  argoSettings.KustomizeOptions,
		KubeVersion:       cluster.Info.ServerVersion,
		ApiVersions:       cluster.Info.APIVersions,
		TrackingMethod:    argoSettings.TrackingMethod,
	}, true, &git.NoopCredsStore{}, resource.MustParse("0"), nil)
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
