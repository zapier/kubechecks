package generator

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

	argogenerator "github.com/argoproj/argo-cd/v3/applicationset/generators"
	"github.com/argoproj/argo-cd/v3/applicationset/utils"
	argov1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// sampleParamsKey marks a param set as synthetic. It survives Matrix and Merge combination, so any
// Application rendered from it can be identified as a preview rather than a real, checkable app.
const sampleParamsKey = "__kubechecks_sample_params"

func isSampleParams(params map[string]any) bool {
	_, ok := params[sampleParamsKey]
	return ok
}

// samplePullRequestGenerator stands in for Argo CD's PullRequest generator, which needs SCM API access.
// It validates the generator config and emits a single, realistic pull request so the template can
// still be rendered and validated.
type samplePullRequestGenerator struct{}

func (samplePullRequestGenerator) GenerateParams(appSetGenerator *argov1alpha1.ApplicationSetGenerator, appSet *argov1alpha1.ApplicationSet, _ client.Client) ([]map[string]any, error) {
	if appSetGenerator == nil || appSetGenerator.PullRequest == nil {
		return nil, argogenerator.ErrEmptyAppSetGenerator
	}
	pr := appSetGenerator.PullRequest

	if err := validatePullRequestProvider(pr); err != nil {
		return nil, err
	}

	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	params := map[string]any{
		sampleParamsKey:      true,
		"number":             "1234",
		"title":              "kubechecks sample pull request",
		"branch":             "kubechecks/sample-branch",
		"branch_slug":        "kubechecks-sample-branch",
		"target_branch":      "main",
		"target_branch_slug": "main",
		"head_sha":           headSHA,
		"head_short_sha":     headSHA[:8],
		"head_short_sha_7":   headSHA[:7],
		"author":             "kubechecks",
	}
	if appSet.Spec.GoTemplate {
		params["labels"] = samplePullRequestLabels(pr)
	}

	if err := appendTemplatedValues(pr.Values, params, appSet.Spec.GoTemplate, appSet.Spec.GoTemplateOptions); err != nil {
		return nil, fmt.Errorf("failed to append templated values: %w", err)
	}

	return []map[string]any{params}, nil
}

func (samplePullRequestGenerator) GetRequeueAfter(*argov1alpha1.ApplicationSetGenerator) time.Duration {
	return argogenerator.NoRequeueAfter
}

func (samplePullRequestGenerator) GetTemplate(appSetGenerator *argov1alpha1.ApplicationSetGenerator) *argov1alpha1.ApplicationSetTemplate {
	return &appSetGenerator.PullRequest.Template
}

// validatePullRequestProvider requires exactly one SCM provider, matching Argo CD's provider selection.
func validatePullRequestProvider(pr *argov1alpha1.PullRequestGenerator) error {
	var providers []string
	v := reflect.ValueOf(pr).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.Kind() != reflect.Pointer || field.IsNil() || field.Elem().Kind() != reflect.Struct {
			continue
		}
		providers = append(providers, strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0])
	}

	switch len(providers) {
	case 0:
		return errors.New("pullRequest generator has no SCM provider configured")
	case 1:
		return nil
	default:
		return fmt.Errorf("pullRequest generator has multiple SCM providers configured: %s", strings.Join(providers, ", "))
	}
}

// samplePullRequestLabels returns the labels the provider filters on, so the sample PR would have matched.
func samplePullRequestLabels(pr *argov1alpha1.PullRequestGenerator) []string {
	var labels []string
	switch {
	case pr.Github != nil:
		labels = pr.Github.Labels
	case pr.GitLab != nil:
		labels = pr.GitLab.Labels
	case pr.Gitea != nil:
		labels = pr.Gitea.Labels
	case pr.AzureDevOps != nil:
		labels = pr.AzureDevOps.Labels
	}
	if len(labels) == 0 {
		return []string{"kubechecks"}
	}
	return labels
}

// appendTemplatedValues mirrors Argo CD's unexported helper of the same name.
func appendTemplatedValues(values map[string]string, params map[string]any, useGoTemplate bool, goTemplateOptions []string) error {
	renderer := utils.Render{}
	tmp := map[string]any{}
	for key, value := range values {
		result, err := renderer.Replace(value, params, useGoTemplate, goTemplateOptions)
		if err != nil {
			return fmt.Errorf("failed to replace templated string with rendered values: %w", err)
		}

		if useGoTemplate {
			if tmp["values"] == nil {
				tmp["values"] = map[string]string{}
			}
			tmp["values"].(map[string]string)[key] = result
		} else {
			tmp["values."+key] = result
		}
	}

	maps.Copy(params, tmp)
	return nil
}

// validateApplication catches the mistakes in a rendered Application that Argo CD would reject.
func validateApplication(app *argov1alpha1.Application) error {
	var errs []error

	for _, msg := range validation.IsDNS1123Subdomain(app.Name) {
		errs = append(errs, fmt.Errorf("invalid name %q: %s", app.Name, msg))
	}

	if app.Spec.Project == "" {
		errs = append(errs, errors.New("spec.project is empty"))
	}

	dest := app.Spec.Destination
	switch {
	case dest.Server == "" && dest.Name == "":
		errs = append(errs, errors.New("spec.destination must set either server or name"))
	case dest.Server != "" && dest.Name != "":
		errs = append(errs, errors.New("spec.destination must not set both server and name"))
	}

	if app.Spec.Source == nil && len(app.Spec.Sources) == 0 && app.Spec.SourceHydrator == nil {
		errs = append(errs, errors.New("spec must set source, sources or sourceHydrator"))
	}

	return errors.Join(errs...)
}
