package kyverno

import (
	"context"
	"fmt"
	"os"

	engineapi "github.com/kyverno/kyverno/pkg/engine/api"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	apply "github.com/zapier/kubechecks/pkg/kyverno-kubectl"
	"github.com/zapier/kubechecks/pkg/msg"
)

var tracer = otel.Tracer("pkg/checks/kyverno")

const check = "kyverno"

const divider = "----------------------------------------------------------------------"

func kyvernoValidate(ctx context.Context, ctr container.Container, appName, targetKubernetesVersion string, appManifests []string) (msg.Result, error) {
	_, span := tracer.Start(ctx, "KyvernoValidate")
	defer span.End()

	log.Debug().Str("check", check).Msg("Creating temporary file for app manifests")
	tempFile, err := os.CreateTemp("/tmp", "appManifests-*.yaml")
	if err != nil {
		log.Error().Str("check", check).Err(err).Msg("Failed to create temporary file")
		return msg.Result{}, err
	}
	// defer os.Remove(tempFile.Name())

	log.Debug().Str("check", check).Str("tempFile", tempFile.Name()).Msg("Temporary file created")
	// log.Debug().Str("check", check).Msgf("App Manifests: %v", appManifests)

	for _, manifest := range appManifests {
		if _, err := tempFile.WriteString(manifest); err != nil {
			log.Error().Str("check", check).Err(err).Msg("Failed to write manifest to temporary file")
			return msg.Result{}, err
		}
	}

	log.Debug().Str("check", check).Str("tempfile", tempFile.Name()).Msg("App manifests written to temporary file")

	if err := tempFile.Close(); err != nil {
		log.Error().Str("check", check).Err(err).Msg("Failed to close temporary file")
		return msg.Result{}, err
	}

	policyPaths := ctr.Config.KyvernoPoliciesLocation
	resourcesPath := []string{tempFile.Name()}
	applyResult := apply.RunKyvernoApply(policyPaths, resourcesPath)
	if applyResult.Error != nil {
		log.Error().Str("check", check).Err(applyResult.Error).Msg("Failed to apply kyverno policies")
		return msg.Result{}, err
	}

	var cr msg.Result
	if applyResult.RC.Fail > 0 || applyResult.RC.Error > 0 {
		cr.State = pkg.StateWarning
	} else {
		cr.State = pkg.StateSuccess
	}
	failedRulesMsg := getFailedRuleMsg(applyResult)

	log.Debug().Str("check", check).Msg("Kyverno validation completed")
	cr.Summary = "<b>Show kyverno report:</b>"
	cr.Details = fmt.Sprintf(`> Kyverno Policy Report

Applied %d policy rule(s) to %d resource(s)...

%s

		pass: %d, fail: %d, warn: %d, error: %d, skip: %d`,
		applyResult.PolicyRuleCount, len(applyResult.Resources),
		failedRulesMsg, applyResult.RC.Pass, applyResult.RC.Fail, applyResult.RC.Warn, applyResult.RC.Error, applyResult.RC.Skip,
	)

	log.Debug().Str("check", check).Msg("Kyverno validation completed")

	return cr, nil
}

func getFailedRuleMsg(applyResult apply.Result) string {
	out := os.Stdout
	failedRulesMsg := ""

	if len(applyResult.SkippedInvalidPolicies.Skipped) > 0 {
		failedRulesMsg += "\n" + divider + "\n"
		fmt.Fprintln(out, "Policies Skipped (as required variables are not provided by the user):")
		failedRulesMsg += "Policies Skipped (as required variables are not provided by the user):\n"
		for i, policyName := range applyResult.SkippedInvalidPolicies.Skipped {
			fmt.Fprintf(out, "%d. %s\n", i+1, policyName)
			failedRulesMsg += fmt.Sprintf("%d. %s\n", i+1, policyName)
		}
		failedRulesMsg += "\n" + divider
	}
	if len(applyResult.SkippedInvalidPolicies.Invalid) > 0 {
		fmt.Fprintln(out, "Invalid Policies:")
		failedRulesMsg += "\nInvalid Policies:\n"
		for i, policyName := range applyResult.SkippedInvalidPolicies.Invalid {
			fmt.Fprintf(out, "%d. %s\n", i+1, policyName)
			failedRulesMsg += fmt.Sprintf("%d. %s\n", i+1, policyName)
		}
		failedRulesMsg += "\n" + divider
	}

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
			failedRulesMsg += fmt.Sprintf("\npolicy `%s` -> resource `%s` failed: \n", response.Policy().GetName(), resPath)
			fmt.Fprintln(out, "policy", response.Policy().GetName(), "->", "resource", resPath, "failed:")
			for i, rule := range failedRules {
				fmt.Fprintln(out, i+1, "-", rule.Name(), rule.Message())
				failedRulesMsg += fmt.Sprintf("\n%d - %s %s \n", i+1, rule.Name(), rule.Message())
			}
			failedRulesMsg += "\n" + divider + "\n"
		}
	}
	return failedRulesMsg
}
