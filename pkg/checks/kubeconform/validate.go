package kubeconform

import (
	"context"
	"fmt"
	"io"
	"os"
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

func getSchemaLocations(ctr container.Container) []string {
	cfg := ctr.Config

	locations := []string{
		// schemas included in kubechecks
		"default",
	}

	// schemas configured globally
	locations = append(locations, cfg.SchemasLocations...)

	for index := range locations {
		location := locations[index]
		oldLocation := location
		if location == "default" || strings.Contains(location, "{{") {
			log.Debug().Str("location", location).Msg("location requires no processing to be valid")
			continue
		}

		if !strings.HasSuffix(location, "/") {
			location += "/"
		}

		location += "{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json"
		locations[index] = location

		log.Debug().Str("old", oldLocation).Str("new", location).Msg("processed schema location")
	}

	return locations
}

func argoCdAppValidate(ctx context.Context, ctr container.Container, appName, targetKubernetesVersion string, appManifests []string) (msg.Result, error) {
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
		Debug:                log.Debug().Enabled(),
	}

	var (
		outputString    []string
		schemaLocations = getSchemaLocations(ctr)
	)

	log.Debug().Msgf("cache location: %s", vOpts.Cache)
	log.Debug().Msgf("target kubernetes version: %s", targetKubernetesVersion)
	log.Debug().Msgf("schema locations: %s", strings.Join(schemaLocations, ", "))

	v, err := validator.New(schemaLocations, vOpts)
	if err != nil {
		return msg.Result{}, fmt.Errorf("could not create kubeconform validator: %v", err)
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
			outputString = append(outputString, fmt.Sprintf(" * :right_arrow: Skipped: %s", sig))
		default:
			outputString = append(outputString, fmt.Sprintf(" * :white_check_mark: Passed: %s", sig))
		}
	}

	var cr msg.Result
	if invalid {
		cr.State = pkg.StateWarning
	} else if failedValidation {
		cr.State = pkg.StateFailure
	} else {
		cr.State = pkg.StateSuccess
	}

	cr.Summary = "<b>Show kubeconform report:</b>"
	cr.Details = fmt.Sprintf(">Validated against Kubernetes Version: %s\n\n%s", targetKubernetesVersion, strings.Join(outputString, "\n"))

	return cr, nil
}
