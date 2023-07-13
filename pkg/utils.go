package pkg

import (
	"fmt"

	argoappv1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/reposerver/apiclient"
	"github.com/ghodss/yaml"
	"github.com/rs/zerolog/log"
)

var (
	GitTag    = ""
	GitCommit = ""
)

func BuildManifest(resp *apiclient.ManifestResponse) []string {
	manifests := []string{}
	for _, m := range resp.Manifests {
		obj, err := argoappv1.UnmarshalToUnstructured(m)
		if err != nil {
			log.Warn().Msgf("error processing Argo manifest: %v", err)
			continue
		}

		yamlBytes, _ := yaml.Marshal(obj)
		manifests = append(manifests, fmt.Sprintf("---\n%s", string(yamlBytes)))
	}

	return manifests
}

func PassEmoji() string {
	return " :white_check_mark: "
}
func PassString() string {
	return " Passed" + PassEmoji()
}

func WarningEmoji() string {
	return " :warning: "
}

func WarningString() string {
	return " Warning" + WarningEmoji()
}

func FailedEmoji() string {
	return " :red_circle: "
}

func FailedString() string {
	return " Failed" + FailedEmoji()
}

func Pointer[T interface{}](item T) *T {
	return &item
}
