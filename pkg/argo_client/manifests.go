package argo_client

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient/cluster"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/settings"
	argoappv1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	repoapiclient "github.com/argoproj/argo-cd/v2/reposerver/apiclient"
	"github.com/argoproj/argo-cd/v2/reposerver/repository"

	// "github.com/argoproj/argo-cd/v2/util/config"
	"github.com/argoproj/argo-cd/v2/util/git"
	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/zapier/kubechecks/telemetry"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
)

// Retrieve token for authentication against ECR registries.
func getToken(aws_ecr_host string) (string, error) {
	os.Setenv("AWS_SDK_LOAD_CONFIG", "1")
	var region = strings.SplitN(string(aws_ecr_host), ".", 6)
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(region[3]))
	if err != nil {
		return "", err
	}

	svc := ecr.NewFromConfig(cfg)
	token, err := svc.GetAuthorizationToken(context.TODO(), &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", err
	}

	authData := token.AuthorizationData[0].AuthorizationToken
	data, err := base64.StdEncoding.DecodeString(*authData)
	if err != nil {
		return "", err
	}

	parts := strings.SplitN(string(data), ":", 2)

	return parts[1], nil
}

func helmLogin(tempRepoDir string, changedAppFilePath string) error {

	var aws_ecr_host = os.Getenv("AWS_ECR_HOST")
	var currToken = ""
	if token, err := getToken(aws_ecr_host); err != nil {
		fmt.Println(err)
	} else {
		currToken = token
	}

	cmd := exec.Command("bash", "-c", "echo "+currToken+" | helm registry login --username AWS --password-stdin "+aws_ecr_host+"; helm dependency build")
	cmd.Dir = tempRepoDir + "/" + changedAppFilePath
	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		log.Fatal()
	}

	fmt.Println("out:", outb.String(), "err:", errb.String())
	return nil
}

func GetManifestsLocal(ctx context.Context, argoClient *ArgoClient, name, tempRepoDir, changedAppFilePath string, app argoappv1.Application) ([]string, error) {
	var err error

	ctx, span := tracer.Start(ctx, "GetManifestsLocal")
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

	source := app.Spec.GetSource()

	s := os.Getenv("ECR_LOGIN_ENABLED")
	ecr_login_enabled, err := strconv.ParseBool(s)
	if err != nil {
		log.Fatal()
	}

	if ecr_login_enabled {
		helmLogin(tempRepoDir, changedAppFilePath)
	}

	s := os.Getenv("ECR_LOGIN_ENABLED")
	ecr_login_enabled, err := strconv.ParseBool(s)
	if err != nil {
		log.Fatal()
	}

	if ecr_login_enabled {
		helmLogin(tempRepoDir, changedAppFilePath)
	}

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
