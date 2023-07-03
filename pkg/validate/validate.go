package validate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"go.opentelemetry.io/otel"

	"github.com/yannh/kubeconform/pkg/validator"
)

const kubeconformCommentFormat = `
<details><summary><b>Show kubeconform report:</b> %s</summary>

>Validated against Kubernetes Version: %s

%s

</details>
`

var schemaLocations = []string{
	`./schemas/{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json`,
	"https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/{{ .NormalizedKubernetesVersion }}-standalone{{ .StrictSuffix }}/{{ .ResourceKind }}{{ .KindSuffix }}.json",
}

func ArgoCdAppValidate(ctx context.Context, appName, targetKubernetesVersion string, appManifests []string) (string, error) {
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
	}

	var outputString []string

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
