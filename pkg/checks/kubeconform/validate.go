package kubeconform

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/yannh/kubeconform/pkg/validator"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/msg"
)

var tracer = otel.Tracer("pkg/checks/kubeconform")

func getSchemaLocations(ctr container.Container, tempRepoPath string) []string {
	cfg := ctr.Config

	locations := []string{
		// schemas included in kubechecks
		"default",
	}

	// schemas configured globally
	for _, schemasLocation := range cfg.SchemasLocations {
		if strings.HasPrefix(schemasLocation, "http://") || strings.HasPrefix(schemasLocation, "https://") {
			locations = append(locations, schemasLocation)
		} else {
			if !filepath.IsAbs(schemasLocation) {
				schemasLocation = filepath.Join(tempRepoPath, schemasLocation)
			}

			if _, err := os.Stat(schemasLocation); err != nil {
				log.Warn().
					Err(err).
					Str("path", schemasLocation).
					Msg("schemas location is invalid, skipping")
			} else {
				locations = append(locations, schemasLocation)
			}
		}
	}

	for index := range locations {
		location := locations[index]
		if location == "default" || strings.Contains(location, "{{") {
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

func argoCdAppValidate(ctx context.Context, ctr container.Container, appName, targetKubernetesVersion, tempRepoPath string, appManifests []string) (msg.Result, error) {
	_, span := tracer.Start(ctx, "ArgoCdAppValidate")
	defer span.End()

	log.Debug().Str("app_name", appName).Str("k8s_version", targetKubernetesVersion).Msg("ArgoCDAppValidate")

	schemaCachePath, err := os.MkdirTemp("", "kubechecks-schema-cache-")
	if err != nil {
		return msg.Result{}, errors.Wrap(err, "failed to create schema cache")
	}
	defer pkg.WipeDir(schemaCachePath)

	vOpts := validator.Opts{
		Cache:   schemaCachePath,
		SkipTLS: false,
		SkipKinds: map[string]struct{}{
			"apiextensions.k8s.io/v1/CustomResourceDefinition": {},
		},
		RejectKinds:          nil,
		KubernetesVersion:    targetKubernetesVersion,
		Strict:               true,
		IgnoreMissingSchemas: false,
		Debug:                log.Debug().Enabled(), //nolint: zerologlint
	}

	var (
		outputString    []string
		schemaLocations = getSchemaLocations(ctr, tempRepoPath)
	)

	log.Debug().Msgf("cache location: %s", vOpts.Cache)
	log.Debug().Msgf("target kubernetes version: %s", targetKubernetesVersion)
	log.Debug().Msgf("schema locations: %s", strings.Join(schemaLocations, ", "))

	v, err := validator.New(schemaLocations, vOpts)
	if err != nil {
		return msg.Result{}, errors.Wrap(err, "could not create kubeconform validator")
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

	var cr msg.Result
	switch {
	case invalid:
		cr.State = pkg.StateWarning
	case failedValidation:
		cr.State = pkg.StateFailure
	default:
		cr.State = pkg.StateSuccess
	}

	cr.Summary = "<b>Show kubeconform report:</b>"
	cr.Details = fmt.Sprintf(">Validated against Kubernetes Version: %s\n\n%s", targetKubernetesVersion, strings.Join(outputString, "\n"))

	return cr, nil
}
