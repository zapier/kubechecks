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

	tempFile, err := os.CreateTemp("/tmp", "appManifests-*.yaml")
	if err != nil {
		return msg.Result{}, err
	}
	defer os.Remove(tempFile.Name())

	for _, manifest := range appManifests {
		if _, err := tempFile.WriteString(manifest + "\n"); err != nil {
			return msg.Result{}, err
		}
	}

	if err := tempFile.Close(); err != nil {
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
		return msg.Result{}, err
	}
	log.Info().Msg(output.String())

	var cr msg.Result
	if output.Len() == 0 {
		cr.State = pkg.StateWarning
	} else {
		cr.State = pkg.StateSuccess
	}

	cr.Summary = "<b>Show kyverno report:</b>"
	cr.Details = fmt.Sprintf(">Kyverno Policy Report \n\n%s", output.String())

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
