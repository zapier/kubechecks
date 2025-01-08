package kyverno

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	engineapi "github.com/kyverno/kyverno/pkg/engine/api"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/msg"
	"go.opentelemetry.io/otel"

	apply "github.com/zapier/kubechecks/pkg/kyverno-kubectl"
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
		if _, err := tempFile.WriteString(manifest + "\n---"); err != nil {
			log.Error().Err(err).Msg("Failed to write manifest to temporary file")
			return msg.Result{}, err
		}
	}

	log.Debug().Msg("App manifests written to temporary file")
	_, _ = io.Copy(os.Stdout, tempFile)

	if err := tempFile.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to close temporary file")
		return msg.Result{}, err
	}

	policyPaths := getPoliciesLocations(ctr)
	resourcesPath := []string{tempFile.Name()}
	applyResult := apply.RunKyvernoApply(policyPaths, resourcesPath)
	if applyResult.Error != nil {
		return msg.Result{}, err
	}

	var cr msg.Result
	if applyResult.RC.Fail > 0 || applyResult.RC.Error > 0 {
		cr.State = pkg.StateWarning
	} else {
		cr.State = pkg.StateSuccess
	}
	out := os.Stdout
	failedRulesMsg := ""

	for _, response := range applyResult.Responses {
		var failedRules []engineapi.RuleResponse
		resPath := fmt.Sprintf("%s/%s/%s", response.Resource.GetNamespace(), response.Resource.GetKind(), response.Resource.GetName())
		for _, rule := range response.PolicyResponse.Rules {
			if rule.Status() == engineapi.RuleStatusFail {
				failedRules = append(failedRules, rule)
			}
			if rule.RuleType() == engineapi.Mutation {
				if rule.Status() == engineapi.RuleStatusSkip {
					fmt.Fprintln(out, "\nskipped mutate policy", response.Policy().GetName(), "->", "resource", resPath)
				} else if rule.Status() == engineapi.RuleStatusError {
					fmt.Fprintln(out, "\nerror while applying mutate policy", response.Policy().GetName(), "->", "resource", resPath, "\nerror: ", rule.Message())
				}
			}
		}
		if len(failedRules) > 0 {
			failedRulesMsg = fmt.Sprintf(failedRulesMsg, "policy %s -> resource %s failed: \n", response.Policy().GetName(), resPath)
			fmt.Fprintln(out, "policy", response.Policy().GetName(), "->", "resource", resPath, "failed:")
			for i, rule := range failedRules {
				fmt.Fprintln(out, i+1, "-", rule.Name(), rule.Message())
				failedRulesMsg = fmt.Sprintf(failedRulesMsg, "%d - %s %s \n", i+1, rule.Name(), rule.Message())
			}
			failedRulesMsg = fmt.Sprintf(failedRulesMsg, "\n")
		}
	}

	log.Debug().Msg("Kyverno validation completed")
	cr.Summary = "<b>Show kyverno report:</b>"
	cr.Details = fmt.Sprintf(`> Kyverno Policy Report \n\n
		%s \n\n
		\npass: %d, fail: %d, warn: %d, error: %d, skip: %d \n`,
		failedRulesMsg, applyResult.RC.Pass, applyResult.RC.Fail, applyResult.RC.Warn, applyResult.RC.Error, applyResult.RC.Skip,
	)

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
