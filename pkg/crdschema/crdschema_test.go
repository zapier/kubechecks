package crdschema

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yannh/kubeconform/pkg/validator"
)

const widgetCRD = `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              required:
                - size
              properties:
                size:
                  type: integer
                color:
                  type: string
                  enum: [red, green]
                replicas:
                  x-kubernetes-int-or-string: true
                extra:
                  type: object
                  x-kubernetes-preserve-unknown-fields: true
`

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
}

func extract(t *testing.T, repoDir string, paths ...string) *Schemas {
	t.Helper()
	schemas, err := Extract(context.Background(), zerolog.Nop(), Options{RepoDir: repoDir, Paths: paths})
	require.NoError(t, err)
	t.Cleanup(schemas.Cleanup)
	return schemas
}

// validate runs the real kubeconform validator against the generated schemas, which is
// the only way to be sure the generated filenames and dialect are what it expects.
func validate(t *testing.T, schemas *Schemas, manifest string, fallbackLocations ...string) validator.Result {
	t.Helper()

	v, err := validator.New(append(schemas.Locations(), fallbackLocations...), validator.Opts{
		KubernetesVersion:    "1.30.0",
		Strict:               true,
		IgnoreMissingSchemas: false,
	})
	require.NoError(t, err)

	results := v.Validate("-", io.NopCloser(strings.NewReader(manifest)))
	require.Len(t, results, 1)
	return results[0]
}

func TestExtractValidatesAResourceAgainstACRDFromTheSameCommit(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "crds/widget.yaml", widgetCRD)

	schemas := extract(t, repoDir)

	require.Len(t, schemas.Schemas(), 1)
	assert.Equal(t, Schema{
		Kind:       "Widget",
		Group:      "example.com",
		Version:    "v1",
		SourceFile: filepath.Join("crds", "widget.yaml"),
	}, schemas.Schemas()[0])

	found, ok := schemas.Lookup("example.com/v1", "Widget")
	assert.True(t, ok)
	assert.Equal(t, "Widget", found.Kind)

	t.Run("accepts a valid resource", func(t *testing.T) {
		result := validate(t, schemas, `
apiVersion: example.com/v1
kind: Widget
metadata:
  name: valid
spec:
  size: 3
  color: red
  replicas: "50%"
  extra:
    anything: goes
`)
		assert.Equal(t, validator.Valid, result.Status, "%v", result.Err)
	})

	// A CRD's openAPIV3Schema does not close its objects, and we pass it through as
	// written, so an unrecognised field is accepted here. That matches how the API server
	// treats one — it prunes the field rather than rejecting the resource — and closing
	// the objects ourselves to catch it would mean rewriting every schema we extract.
	t.Run("accepts an unrecognised field, as the API server does", func(t *testing.T) {
		result := validate(t, schemas, `
apiVersion: example.com/v1
kind: Widget
metadata:
  name: typo
spec:
  size: 3
  collor: red
`)
		assert.Equal(t, validator.Valid, result.Status, "%v", result.Err)
	})

	t.Run("rejects a missing required field", func(t *testing.T) {
		result := validate(t, schemas, `
apiVersion: example.com/v1
kind: Widget
metadata:
  name: incomplete
spec:
  color: red
`)
		assert.Equal(t, validator.Invalid, result.Status)
	})

	t.Run("rejects a wrongly typed field", func(t *testing.T) {
		result := validate(t, schemas, `
apiVersion: example.com/v1
kind: Widget
metadata:
  name: mistyped
spec:
  size: large
`)
		assert.Equal(t, validator.Invalid, result.Status)
	})

	t.Run("rejects a value outside the enum", func(t *testing.T) {
		result := validate(t, schemas, `
apiVersion: example.com/v1
kind: Widget
metadata:
  name: offlist
spec:
  size: 1
  color: purple
`)
		assert.Equal(t, validator.Invalid, result.Status)
	})
}

func TestExtractLeavesUnknownKindsToOtherSchemaLocations(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "crds/widget.yaml", widgetCRD)

	schemas := extract(t, repoDir)

	_, ok := schemas.Lookup("other.com/v1", "Gadget")
	assert.False(t, ok)

	// A kind we did not generate must fall through to the schema locations that follow
	// ours, rather than failing there.
	fallback := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(fallback, "configmap-v1.json"),
		[]byte(`{"type":"object","properties":{"data":{"type":"object"}}}`),
		0o644,
	))

	result := validate(t, schemas, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
data:
  key: value
`, filepath.Join(fallback, "{{ .ResourceKind }}{{ .KindSuffix }}.json"))
	assert.Equal(t, validator.Valid, result.Status, "%v", result.Err)
}

// A schema kubeconform cannot compile must not be offered to it. kubeconform skips an
// uncompilable schema silently and, finding the kind nowhere else, reports "could not
// find schema" — a check failure naming the wrong problem. Dropping it here makes the
// kind resolve through the remaining locations, exactly as if the repository had never
// defined it. `required: []` is the readiest example: legal in a CRD, rejected by
// draft-04.
func TestExtractDropsSchemasKubeconformCannotCompile(t *testing.T) {
	const uncompilableCRD = `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gizmos.example.com
spec:
  group: example.com
  names:
    kind: Gizmo
    plural: gizmos
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          required: []
          properties:
            spec:
              type: object
`

	repoDir := t.TempDir()
	writeFile(t, repoDir, "crds/widget.yaml", widgetCRD)
	writeFile(t, repoDir, "crds/gizmo.yaml", uncompilableCRD)

	schemas := extract(t, repoDir)

	_, ok := schemas.Lookup("example.com/v1", "Gizmo")
	assert.False(t, ok, "an uncompilable schema must not be reported as generated")
	assert.NoFileExists(t, filepath.Join(schemas.dir, SchemaFileName("Gizmo", "example.com", "v1")))

	// The rest of the repository's CRDs are unaffected.
	_, ok = schemas.Lookup("example.com/v1", "Widget")
	assert.True(t, ok)

	// And the dropped kind resolves through a later schema location.
	fallback := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(fallback, SchemaFileName("Gizmo", "example.com", "v1")),
		[]byte(`{"type":"object","properties":{"spec":{"type":"object"}}}`),
		0o644,
	))

	result := validate(t, schemas, `
apiVersion: example.com/v1
kind: Gizmo
metadata:
  name: g
spec: {}
`, filepath.Join(fallback, "{{ .ResourceKind }}{{ .KindSuffix }}.json"))
	assert.Equal(t, validator.Valid, result.Status, "%v", result.Err)
}

func TestExtractHandlesMultipleVersionsAndDocuments(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "bundle.yaml", `
apiVersion: v1
kind: Namespace
metadata:
  name: widgets
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
  versions:
    - name: v1alpha1
      schema:
        openAPIV3Schema:
          type: object
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
`)

	schemas := extract(t, repoDir)

	require.Len(t, schemas.Schemas(), 2)
	assert.Equal(t, "v1", schemas.Schemas()[0].Version)
	assert.Equal(t, "v1alpha1", schemas.Schemas()[1].Version)
}

func TestExtractUnwrapsLists(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "list.yaml", `
apiVersion: v1
kind: List
items:
  - apiVersion: apiextensions.k8s.io/v1
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
`)

	schemas := extract(t, repoDir)
	require.Len(t, schemas.Schemas(), 1)
	assert.Equal(t, "Widget", schemas.Schemas()[0].Kind)
}

func TestExtractSupportsV1Beta1Validation(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "legacy.yaml", `
apiVersion: apiextensions.k8s.io/v1beta1
kind: CustomResourceDefinition
metadata:
  name: gadgets.legacy.io
spec:
  group: legacy.io
  version: v1beta1
  names:
    kind: Gadget
  validation:
    openAPIV3Schema:
      type: object
      properties:
        spec:
          type: object
          properties:
            enabled:
              type: boolean
`)

	schemas := extract(t, repoDir)
	require.Len(t, schemas.Schemas(), 1)
	assert.Equal(t, "legacy.io/v1beta1", schemas.Schemas()[0].APIVersion())

	result := validate(t, schemas, `
apiVersion: legacy.io/v1beta1
kind: Gadget
metadata:
  name: g
spec:
  enabled: true
`)
	assert.Equal(t, validator.Valid, result.Status, "%v", result.Err)
}

func TestExtractSkipsUnparseableAndIrrelevantFiles(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "crds/widget.yaml", widgetCRD)
	// A Helm template mentioning the kind, which is not valid YAML on its own.
	writeFile(t, repoDir, "chart/templates/crd.yaml", `
{{- if .Values.installCRDs }}
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
{{- end }}
`)
	writeFile(t, repoDir, "README.md", "This repo has a CustomResourceDefinition in it.")
	writeFile(t, repoDir, "deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\n")

	schemas := extract(t, repoDir)
	require.Len(t, schemas.Schemas(), 1)
	assert.Equal(t, "Widget", schemas.Schemas()[0].Kind)
}

func TestExtractPrefersTheFirstDefinitionOfAKind(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "a/widget.yaml", widgetCRD)
	writeFile(t, repoDir, "b/widget.yaml", widgetCRD)

	schemas := extract(t, repoDir)
	require.Len(t, schemas.Schemas(), 1)
	assert.Equal(t, filepath.Join("a", "widget.yaml"), schemas.Schemas()[0].SourceFile)
}

func TestExtractHonorsConfiguredPaths(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "crds/widget.yaml", widgetCRD)
	writeFile(t, repoDir, "elsewhere/gadget.yaml", strings.NewReplacer(
		"widgets", "gadgets", "Widget", "Gadget",
	).Replace(widgetCRD))

	schemas := extract(t, repoDir, "crds")
	require.Len(t, schemas.Schemas(), 1)
	assert.Equal(t, "Widget", schemas.Schemas()[0].Kind)
}

func TestExtractSearchesTheWholeRepositoryForDot(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "deep/nested/widget.yaml", widgetCRD)

	schemas := extract(t, repoDir, ".")
	require.Len(t, schemas.Schemas(), 1)
	assert.Equal(t, "Widget", schemas.Schemas()[0].Kind)
}

// A configured path that is not a directory here is reported and stepped over rather
// than failing the extraction: a path can exist on some branches and not others. It is
// also the likeliest explanation for finding no CRDs, so it must not pass silently.
func TestExtractReportsUnusablePathsAndContinues(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "crds/widget.yaml", widgetCRD)
	writeFile(t, repoDir, "notadir.yaml", widgetCRD)

	var logs bytes.Buffer
	schemas, err := Extract(context.Background(), zerolog.New(&logs), Options{
		RepoDir: repoDir,
		Paths:   []string{"crds", "typo/missing", "notadir.yaml"},
	})
	require.NoError(t, err)
	t.Cleanup(schemas.Cleanup)

	// The usable path is still searched.
	require.Len(t, schemas.Schemas(), 1)
	assert.Equal(t, "Widget", schemas.Schemas()[0].Kind)

	// And both unusable ones are named in the log.
	assert.Contains(t, logs.String(), "typo/missing")
	assert.Contains(t, logs.String(), "notadir.yaml")
}

// Finding nothing must say enough to tell a mistyped path from a directory that simply
// holds no CRDs — a directory of pre-built JSON schemas, say, which is not a CRD and
// cannot be treated as one.
func TestExtractExplainsFindingNothing(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, ".github/schemas/autoscalingrunnerset_v1alpha1.json",
		`{"type":"object","properties":{"spec":{"type":"object"}}}`)

	var logs bytes.Buffer
	schemas, err := Extract(context.Background(), zerolog.New(&logs), Options{
		RepoDir: repoDir,
		Paths:   []string{".github/schemas"},
	})
	require.NoError(t, err)
	t.Cleanup(schemas.Cleanup)

	assert.Empty(t, schemas.Schemas())
	assert.Contains(t, logs.String(), "no CustomResourceDefinitions found")
	// The counts are what separate "wrong path" from "right path, nothing to extract".
	assert.Contains(t, logs.String(), `"files_considered":1`)
	assert.Contains(t, logs.String(), `"files_mentioning_crds":0`)
}

func TestExtractRejectsPathsOutsideTheRepository(t *testing.T) {
	_, err := Extract(context.Background(), zerolog.Nop(), Options{
		RepoDir: t.TempDir(),
		Paths:   []string{"../../etc"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the repository")
}

func TestNoSchemasYieldsNoLocations(t *testing.T) {
	schemas := extract(t, t.TempDir())
	assert.Empty(t, schemas.Locations())
	assert.Empty(t, schemas.Location())

	// The nil case stands in for the feature being disabled.
	var disabled *Schemas
	assert.Empty(t, disabled.Locations())
	_, ok := disabled.Lookup("example.com/v1", "Widget")
	assert.False(t, ok)
	disabled.Cleanup()
}

func TestCleanupRemovesGeneratedFiles(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, repoDir, "crds/widget.yaml", widgetCRD)

	schemas, err := Extract(context.Background(), zerolog.Nop(), Options{RepoDir: repoDir})
	require.NoError(t, err)

	dir := schemas.dir
	require.FileExists(t, filepath.Join(dir, "widget-example-v1.json"))

	schemas.Cleanup()
	assert.NoDirExists(t, dir)

	// Calling it twice must not panic or remove anything else.
	schemas.Cleanup()
}
