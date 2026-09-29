package kubeconform

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/crdschema"
)

func TestDefaultGetSchemaLocations(t *testing.T) {
	ctr := container.Container{}
	schemaLocations := getSchemaLocations(ctr, nil)

	// default schema location is "./schemas"
	assert.Len(t, schemaLocations, 1)
	assert.Equal(t, "default", schemaLocations[0])
}

func TestRepoSchemaLocationsComeFirst(t *testing.T) {
	ctr := container.Container{}
	ctr.Config.SchemasLocations = []string{"/etc/schemas"}

	schemaLocations := getSchemaLocations(ctr, []string{"/tmp/crds/{{ .ResourceKind }}{{ .KindSuffix }}.json"})

	// A CRD carried by the commit under test must win over anything configured globally.
	assert.Equal(t, []string{
		"/tmp/crds/{{ .ResourceKind }}{{ .KindSuffix }}.json",
		"default",
		"/etc/schemas/{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json",
	}, schemaLocations)
}

const widgetCRD = `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              required: [size]
              properties:
                size:
                  type: integer
`

// extractWidgetCRD stands in for a pull request that adds a CRD to the repository.
func extractWidgetCRD(t *testing.T) *crdschema.Schemas {
	t.Helper()

	repoDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "widget-crd.yaml"), []byte(widgetCRD), 0o644))

	schemas, err := crdschema.Extract(context.Background(), zerolog.Nop(), crdschema.Options{RepoDir: repoDir})
	require.NoError(t, err)
	t.Cleanup(schemas.Cleanup)

	return schemas
}

func TestValidateAgainstACRDFromTheSameCommit(t *testing.T) {
	schemas := extractWidgetCRD(t)

	result, err := argoCdAppValidate(context.Background(), container.Container{}, "widgets", "1.30.0", []string{`
apiVersion: example.com/v1
kind: Widget
metadata:
  name: one
spec:
  size: 3
`}, schemas)
	require.NoError(t, err)

	assert.Equal(t, pkg.StateSuccess, result.State)
	assert.Contains(t, result.Details, "Validated against 1 CustomResourceDefinition(s) from this commit")
	assert.Contains(t, result.Details, "`Widget` `example.com/v1` (`widget-crd.yaml`)")
	assert.Contains(t, result.Details, "Passed: example.com/v1 Widget one")
}

func TestValidateReportsAResourceThatViolatesItsOwnCRD(t *testing.T) {
	schemas := extractWidgetCRD(t)

	result, err := argoCdAppValidate(context.Background(), container.Container{}, "widgets", "1.30.0", []string{`
apiVersion: example.com/v1
kind: Widget
metadata:
  name: one
spec:
  size: enormous
`}, schemas)
	require.NoError(t, err)

	assert.Equal(t, pkg.StateWarning, result.State)
	assert.Contains(t, result.Details, "**Invalid**: example.com/v1 Widget one")
}

func TestValidateOmitsTheCRDSectionWhenNoneWereUsed(t *testing.T) {
	// A repository may carry CRDs that this particular app never instantiates.
	schemas := extractWidgetCRD(t)

	result, err := argoCdAppValidate(context.Background(), container.Container{}, "widgets", "1.30.0", nil, schemas)
	require.NoError(t, err)

	assert.NotContains(t, result.Details, "from this commit")
}
