package generator

import (
	"fmt"
	"reflect"
	"time"

	argogenerator "github.com/argoproj/argo-cd/v3/applicationset/generators"
	argov1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// generatorNames are the ApplicationSetGenerator field names Argo CD looks generators up by. Argo CD
// dereferences the lookup result without a nil check, so every one of these must be present in the
// generator map or an appset using it will panic.
var generatorNames = func() []string {
	var names []string
	t := reflect.TypeFor[argov1alpha1.ApplicationSetGenerator]()
	for i := range t.NumField() {
		field := t.Field(i)
		// Selector post-filters the generated params; it is not a generator (argo's selectorKey).
		if !field.IsExported() || field.Name == "Selector" {
			continue
		}
		names = append(names, field.Name)
	}
	return names
}()

// withUnsupportedGenerators registers a stub for every generator type missing from generators,
// leaving the ones that were provided untouched.
func withUnsupportedGenerators(generators map[string]argogenerator.Generator) map[string]argogenerator.Generator {
	for _, name := range generatorNames {
		if _, ok := generators[name]; !ok {
			generators[name] = unsupportedGenerator{name: name}
		}
	}
	return generators
}

// UnsupportedGeneratorError is returned when an ApplicationSet uses a generator type kubechecks cannot
// evaluate. It is a limitation of kubechecks rather than a problem with the ApplicationSet, so callers
// should report it without failing the check.
type UnsupportedGeneratorError struct {
	Generator string
}

func (e UnsupportedGeneratorError) Error() string {
	return fmt.Sprintf("%s generator is not supported", e.Generator)
}

// unsupportedGenerator satisfies argogenerator.Generator for generator types kubechecks does not support,
// turning them into an error instead of a nil pointer dereference.
type unsupportedGenerator struct {
	name string
}

func (u unsupportedGenerator) GenerateParams(*argov1alpha1.ApplicationSetGenerator, *argov1alpha1.ApplicationSet, client.Client) ([]map[string]any, error) {
	return nil, UnsupportedGeneratorError{Generator: u.name}
}

func (u unsupportedGenerator) GetRequeueAfter(*argov1alpha1.ApplicationSetGenerator) time.Duration {
	return argogenerator.NoRequeueAfter
}

func (u unsupportedGenerator) GetTemplate(*argov1alpha1.ApplicationSetGenerator) *argov1alpha1.ApplicationSetTemplate {
	return &argov1alpha1.ApplicationSetTemplate{}
}
