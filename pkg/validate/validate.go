package validate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/yannh/kubeconform/pkg/validator"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/local"
)

var reposCache = local.NewReposDirectory()

const kubeconformCommentFormat = `
<details><summary><b>Show kubeconform report:</b> %s</summary>

>Validated against Kubernetes Version: %s

%s

</details>
`

func getSchemaLocations(ctx context.Context, tempRepoPath string) []string {
	locations := []string{
		// schemas included in kubechecks
		"default",
	}

	// schemas configured globally
	schemasLocations := viper.GetStringSlice("schemas-location")
	for _, schemasLocation := range schemasLocations {
		log.Debug().Str("schemas-location", schemasLocation).Msg("viper")
		schemaPath := reposCache.EnsurePath(ctx, tempRepoPath, schemasLocation)
		if schemaPath != "" {
			locations = append(locations, schemaPath)
		}
	}

	for index := range locations {
		location := locations[index]
		if location == "default" {
			continue
		}

		if !strings.HasSuffix(location, "/") {
			location += "/"
		}

		location += "{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json"
		locations[index] = location
	}

	return locations
}

func ArgoCdAppValidate(ctx context.Context, appName, targetKubernetesVersion, tempRepoPath string, appManifests []string) (string, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "ArgoCdAppValidate")
	defer span.End()

	log.Debug().Str("app_name", appName).Str("k8s_version", targetKubernetesVersion).Msg("ArgoCDAppValidate")

	cwd, _ := os.Getwd()
	vOpts := validator.Opts{
		Cache:   filepath.Join(cwd, "schemas/"),
		SkipTLS: false,
		SkipKinds: map[string]struct{}{
			"apiextensions.k8s.io/v1/CustomResourceDefinition": {},
		},
		RejectKinds:          nil,
		KubernetesVersion:    targetKubernetesVersion,
		Strict:               true,
		IgnoreMissingSchemas: false,
		Debug:                log.Debug().Enabled(),
	}

	var (
		outputString    []string
		schemaLocations = getSchemaLocations(ctx, tempRepoPath)
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
