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
	"github.com/zapier/kubechecks/pkg/crdschema"
	"github.com/zapier/kubechecks/pkg/msg"
)

var tracer = otel.Tracer("pkg/checks/kubeconform")

func getSchemaLocations(ctr container.Container, repoLocations []string) []string {
	cfg := ctr.Config

	// schemas generated from the commit under test come first, so a CRD added in the
	// pull request wins over an older copy of the same kind published elsewhere
	locations := append([]string{}, repoLocations...)

	locations = append(locations,
		// schemas included in kubechecks
		"default",
	)

	// schemas configured globally
	locations = append(locations, cfg.SchemasLocations...)

	for index := range locations {
		location := locations[index]
		oldLocation := location
		if location == "default" || strings.Contains(location, "{{") {
			log.Debug().Caller().Str("location", location).Msg("location requires no processing to be valid")
			continue
		}

		if !strings.HasSuffix(location, "/") {
			location += "/"
		}

		location += "{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json"
		locations[index] = location

		log.Debug().Caller().Str("old", oldLocation).Str("new", location).Msg("processed schema location")
	}

	return locations
}

func argoCdAppValidate(ctx context.Context, ctr container.Container, appName, targetKubernetesVersion string, appManifests []string, repoCRDSchemas *crdschema.Schemas) (msg.Result, error) {
	_, span := tracer.Start(ctx, "ArgoCdAppValidate")
	defer span.End()

	log.Debug().
		Caller().
		Str("app_name", appName).
		Str("k8s_version", targetKubernetesVersion).
		Msg("ArgoCDAppValidate")

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
		Debug:                log.Debug().Caller().Enabled(),
	}

	var (
		outputString    []string
		usedRepoCRDs    []crdschema.Schema
		schemaLocations = getSchemaLocations(ctr, repoCRDSchemas.Locations())
	)

	log.Debug().Caller().Msgf("cache location: %s", vOpts.Cache)
	log.Debug().Caller().Msgf("target kubernetes version: %s", targetKubernetesVersion)
	log.Debug().Caller().Msgf("schema locations: %s", strings.Join(schemaLocations, ", "))

	v, err := validator.New(schemaLocations, vOpts)
	if err != nil {
		return msg.Result{}, fmt.Errorf("could not create kubeconform validator: %v", err)
	}
	result := v.Validate("-", io.NopCloser(strings.NewReader(strings.Join(appManifests, "\n"))))
	var invalid, failedValidation bool
	for _, res := range result {
		sigData, _ := res.Resource.Signature()
		sig := fmt.Sprintf("%s %s %s", sigData.Version, sigData.Kind, sigData.Name)

		// Record which of the commit's own CRDs this app exercised, so the report can
		// show that the definition and its instance were checked against each other.
		if crd, ok := repoCRDSchemas.Lookup(sigData.Version, sigData.Kind); ok {
			usedRepoCRDs = appendUnique(usedRepoCRDs, crd)
		}

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
	cr.Details = fmt.Sprintf(">Validated against Kubernetes Version: %s\n%s\n%s",
		targetKubernetesVersion, describeRepoCRDs(usedRepoCRDs), strings.Join(outputString, "\n"))

	return cr, nil
}

// appendUnique keeps the list of exercised CRDs free of duplicates; an app will usually
// hold several instances of the same custom resource.
func appendUnique(schemas []crdschema.Schema, schema crdschema.Schema) []crdschema.Schema {
	for _, existing := range schemas {
		if existing == schema {
			return schemas
		}
	}
	return append(schemas, schema)
}

// describeRepoCRDs renders the CRDs from the commit under test that this app's resources
// were validated against, so a pull request adding a CRD alongside an instance of it can
// see that the two were checked together.
func describeRepoCRDs(schemas []crdschema.Schema) string {
	if len(schemas) == 0 {
		return ""
	}

	lines := make([]string, 0, len(schemas)+1)
	lines = append(lines, fmt.Sprintf(">Validated against %d CustomResourceDefinition(s) from this commit:", len(schemas)))
	for _, schema := range schemas {
		lines = append(lines, fmt.Sprintf(">  * `%s` `%s` (`%s`)", schema.Kind, schema.APIVersion(), schema.SourceFile))
	}

	return strings.Join(lines, "\n") + "\n"
}
