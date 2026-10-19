package generator

import (
	"fmt"
	"time"

	argogenerator "github.com/argoproj/argo-cd/v3/applicationset/generators"
	argov1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// unsupportedGeneratorNames are the ApplicationSetGenerator field names that kubechecks cannot evaluate.
// Argo CD looks generators up by field name and dereferences the result without a nil check, so every
// generator type must be present in the map or an appset using it will panic.
var unsupportedGeneratorNames = []string{"Git", "SCMProvider", "PullRequest", "ClusterDecisionResource", "Plugin"}

// withUnsupportedGenerators registers a stub for every generator type kubechecks cannot evaluate.
func withUnsupportedGenerators(generators map[string]argogenerator.Generator) map[string]argogenerator.Generator {
	for _, name := range unsupportedGeneratorNames {
		generators[name] = unsupportedGenerator{name: name}
	}
	return generators
}

// unsupportedGenerator satisfies argogenerator.Generator for generator types kubechecks does not support,
// turning them into an error instead of a nil pointer dereference.
type unsupportedGenerator struct {
	name string
}

func (u unsupportedGenerator) GenerateParams(*argov1alpha1.ApplicationSetGenerator, *argov1alpha1.ApplicationSet, client.Client) ([]map[string]any, error) {
	return nil, fmt.Errorf("%s generator is not supported", u.name)
}

func (u unsupportedGenerator) GetRequeueAfter(*argov1alpha1.ApplicationSetGenerator) time.Duration {
	return argogenerator.NoRequeueAfter
}

func (u unsupportedGenerator) GetTemplate(*argov1alpha1.ApplicationSetGenerator) *argov1alpha1.ApplicationSetTemplate {
	return &argov1alpha1.ApplicationSetTemplate{}
}
