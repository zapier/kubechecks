package kyverno

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/msg"
	"go.opentelemetry.io/otel"

	"github.com/kyverno/kyverno/cmd/cli/kubectl-kyverno/commands/apply"
)

var tracer = otel.Tracer("pkg/checks/kyverno")

func kyvernoValidate(ctx context.Context, ctr container.Container, appName, targetKubernetesVersion string, appManifests []string) (msg.Result, error) {
	_, span := tracer.Start(ctx, "KyvernoValidate")
	defer span.End()

	log.Debug().Msg("Creating temporary file for app manifests")
	tempFile, err := os.CreateTemp("/tmp", "appManifests-*.yaml")
	if err != nil {
		log.Error().Err(err).Msg("Failed to create temporary file")
		return msg.Result{}, err
	}
	defer os.Remove(tempFile.Name())

	log.Debug().Str("tempFile", tempFile.Name()).Msg("Temporary file created")

	for _, manifest := range appManifests {
		if _, err := tempFile.WriteString(manifest + "\n"); err != nil {
			log.Error().Err(err).Msg("Failed to write manifest to temporary file")
			return msg.Result{}, err
		}
	}

	if err := tempFile.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to close temporary file")
		return msg.Result{}, err
	}

	// This calls the kyverno apply -r <RESOURCE_FILE> <POLICY LOCATIONS ...> command
	applyCommand := apply.Command()
	applyCommand.SetArgs(
		append(
			getPoliciesLocations(ctr),
			[]string{"-r", tempFile.Name()}...))
	var output strings.Builder
	applyCommand.SetOutput(&output)
	if err := applyCommand.Execute(); err != nil {
		log.Error().Err(err).Msg("Failed to execute kyverno apply command")
		return msg.Result{}, err
	}
	log.Info().Msg(output.String())

	var cr msg.Result
	if output.Len() == 0 {
		cr.State = pkg.StateWarning
	} else {
		cr.State = pkg.StateSuccess
	}

	log.Debug().Str("report", output.String()).Msg("Kyverno validation completed")
	cr.Summary = "<b>Show kyverno report:</b>"
	cr.Details = fmt.Sprintf(">Kyverno Policy Report \n\n%s", output.String())

	log.Debug().Msg("Kyverno validation completed")

	return cr, nil
}

func getPoliciesLocations(ctr container.Container) []string {
	cfg := ctr.Config

	// schemas configured globally
	var locations []string

	for _, location := range cfg.KyvernoPoliciesLocation {
		for _, path := range cfg.KyvernoPoliciesPaths {
			locations = append(locations, filepath.Join(location, path))
		}
	}

	log.Debug().Strs("locations", locations).Msg("processed kyverno policies locations")

	return locations
}
