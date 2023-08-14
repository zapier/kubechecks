package validate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/yannh/kubeconform/pkg/validator"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/telemetry"
)

var getSchemasOnce sync.Once // used to ensure we don't reauth this
var refreshSchemasOnce sync.Once

const kubeconformCommentFormat = `
<details><summary><b>Show kubeconform report:</b> %s</summary>

>Validated against Kubernetes Version: %s

%s

</details>
`
const inRepoSchemaLocation = "./schemas"

var localSchemasLocation string

func getSchemaLocations(tempRepoPath string) []string {
	getSchemasOnce.Do(func() {
		ctx := context.Background()
		_, span := otel.Tracer("Kubechecks").Start(ctx, "GetSchemaLocations")
		schemasLocation := viper.GetString("schemas-location")

		var oldLocalSchemasLocation string
		// Store the oldSchemasLocation for clean up afterward
		if localSchemasLocation != inRepoSchemaLocation {
			oldLocalSchemasLocation = localSchemasLocation
		}

		localSchemasLocation = inRepoSchemaLocation
		// if this is a git repo, we want to clone it locally
		if strings.HasPrefix(schemasLocation, "https://") || strings.HasPrefix(schemasLocation, "http://") || strings.HasPrefix(schemasLocation, "git@") {

			tmpSchemasLocalDir, err := os.MkdirTemp("/tmp", "schemas")
			if err != nil {
				log.Err(err).Msg("failed to make temporary directory for downloading schemas")
				telemetry.SetError(span, err, "failed to make temporary directory for downloading schemas")
				return
			}

			r := repo.Repo{CloneURL: schemasLocation}
			err = r.CloneRepoLocal(ctx, tmpSchemasLocalDir)
			if err != nil {
				telemetry.SetError(span, err, "failed to clone schemas repository")
				log.Err(err).Msg("failed to clone schemas repository")
				return
			}

			log.Debug().Str("schemas-repo", schemasLocation).Msg("Cloned schemas Repo to /tmp/schemas")
			localSchemasLocation = tmpSchemasLocalDir

			err = os.RemoveAll(oldLocalSchemasLocation)
			if err != nil {
				telemetry.SetError(span, err, "failed to clean up old schemas directory")
				log.Err(err).Msg("failed to clean up old schemas directory")
			}

			// This is a little function to allow getSchemaLocations to refresh daily by resetting the sync.Once mutex
			refreshSchemasOnce.Do(func() {
				c := cron.New()
				c.AddFunc("@daily", func() {
					getSchemasOnce = *new(sync.Once)
				})
				c.Start()
			})
		}
	})

	locations := []string{
		localSchemasLocation + `/{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json`,
		"https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/{{ .NormalizedKubernetesVersion }}-standalone{{ .StrictSuffix }}/{{ .ResourceKind }}{{ .KindSuffix }}.json",
	}

	// bring in schemas that might be in the cloned repository
	schemaPath := filepath.Join(tempRepoPath, inRepoSchemaLocation)
	if stat, err := os.Stat(schemaPath); err == nil && stat.IsDir() {
		locations = append(locations, schemaPath)
	}

	return locations
}

func ArgoCdAppValidate(ctx context.Context, appName, targetKubernetesVersion, tempRepoPath string, appManifests []string) (string, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "ArgoCdAppValidate")
	defer span.End()

	log.Debug().Str("app_name", appName).Str("k8s_version", targetKubernetesVersion).Msg("ArgoCDAppValidate")

	cwd, _ := os.Getwd()
	vOpts := validator.Opts{
		Cache:                filepath.Join(cwd, "schemas/"),
		SkipTLS:              false,
		SkipKinds:            nil,
		RejectKinds:          nil,
		KubernetesVersion:    targetKubernetesVersion,
		Strict:               true,
		IgnoreMissingSchemas: false,
		Debug:                log.Debug().Enabled(),
	}

	var (
		outputString    []string
		schemaLocations = getSchemaLocations(tempRepoPath)
	)

	log.Debug().Msgf("cache location: %s", vOpts.Cache)
	log.Debug().Msgf("target kubernetes version: %s", targetKubernetesVersion)
	log.Debug().Msgf("schema locations: %s", strings.Join(schemaLocations, ", "))

	v, err := validator.New(schemaLocations, vOpts)
	if err != nil {
		log.Error().Err(err).Msg("could not create kubeconform validator")
		return "", fmt.Errorf("could not create kubeconform validator: %v", err)
	}
	result := v.Validate("-", io.NopCloser(strings.NewReader(strings.Join(appManifests, "\n"))))
	var invalid, failedValidation bool
	for _, res := range result {
		sigData, _ := res.Resource.Signature()
		sig := fmt.Sprintf("%s %s %s", sigData.Version, sigData.Kind, sigData.Name)

		switch res.Status {
		case validator.Invalid:
			outputString = append(outputString, fmt.Sprintf(" * :warning: **Invalid**: %s", sig))
			outputString = append(outputString, fmt.Sprintf("   * %s ", res.Err))
			invalid = true
		case validator.Error:
			outputString = append(outputString, fmt.Sprintf(" * :red_circle: **Error**: %s - %v", sig, res.Err))
			failedValidation = true
		case validator.Empty:
			// noop
		case validator.Skipped:
			outputString = append(outputString, fmt.Sprintf(" * :skip: Skipped: %s", sig))
		default:
			outputString = append(outputString, fmt.Sprintf(" * :white_check_mark: Passed: %s", sig))
		}
	}
	summary := pkg.PassString()
	if invalid {
		summary = pkg.WarningString()
	} else if failedValidation {
		summary = pkg.FailedString()
	}

	return fmt.Sprintf(kubeconformCommentFormat, summary, targetKubernetesVersion, strings.Join(outputString, "\n")), nil
}
