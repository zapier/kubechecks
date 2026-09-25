package kubeconform

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
)

func TestDefaultGetSchemaLocations(t *testing.T) {
	ctr := container.Container{}
	schemaLocations := getSchemaLocations(ctr, "")

	// default schema location is "./schemas"
	assert.Len(t, schemaLocations, 1)
	assert.Equal(t, "default", schemaLocations[0])
}

// Relative and absolute locations sit in one list and are searched in the order they
// were configured. A relative one is rooted at the checkout but is otherwise treated
// exactly like the rest, including the Kubernetes version segment a plain directory
// gets — a repository organising its schemas by kind alone says so with a template.
func TestGetSchemaLocationsMixesRelativeAndAbsolute(t *testing.T) {
	repoDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".github", "schemas"), 0o755))

	ctr := container.Container{Config: config.ServerConfig{
		SchemasLocations: []string{".github/schemas", "/global/schemas"},
	}}

	assert.Equal(t, []string{
		"default",
		filepath.Join(repoDir, ".github", "schemas") + "/{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json",
		"/global/schemas/{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json",
	}, getSchemaLocations(ctr, repoDir))
}

func TestResolveSchemaLocations(t *testing.T) {
	repoDir := t.TempDir()
	for _, dir := range []string{".github/schemas", "nested/deeper"} {
		require.NoError(t, os.MkdirAll(filepath.Join(repoDir, filepath.FromSlash(dir)), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "afile.txt"), []byte("x"), 0o644))

	tests := []struct {
		name       string
		configured []string
		want       []string
	}{
		{
			name:       "a relative directory is rooted at the checkout",
			configured: []string{".github/schemas"},
			want:       []string{filepath.Join(repoDir, ".github/schemas")},
		},
		{
			// the CRDs-catalog layout, which openapi2jsonschema also produces
			name:       "a relative template is rooted and left otherwise alone",
			configured: []string{".github/schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json"},
			want:       []string{filepath.Join(repoDir, ".github/schemas", "{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json")},
		},
		{
			name:       "a template may sit inside a filename",
			configured: []string{"nested/deeper/crd-{{ .ResourceKind }}.json"},
			want:       []string{filepath.Join(repoDir, "nested/deeper", "crd-{{ .ResourceKind }}.json")},
		},
		{
			name:       "an absolute path is left alone",
			configured: []string{"/host/schemas"},
			want:       []string{"/host/schemas"},
		},
		{
			// already cloned to a local directory by processLocations, but a raw one
			// must not be mistaken for a path inside the checkout either
			name:       "a git url is left alone",
			configured: []string{"git@github.com:org/schemas.git"},
			want:       []string{"git@github.com:org/schemas.git"},
		},
		{
			name:       "an http location is left alone",
			configured: []string{"https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"},
			want:       []string{"https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"},
		},
		{
			name:       "locations keep their configured order",
			configured: []string{".github/schemas", "/host/schemas", "nested/deeper"},
			want: []string{
				filepath.Join(repoDir, ".github/schemas"),
				"/host/schemas",
				filepath.Join(repoDir, "nested/deeper"),
			},
		},
		{
			name:       "a directory that is not in this commit is dropped",
			configured: []string{"does/not/exist"},
			want:       nil,
		},
		{
			name:       "a file is not a schema directory",
			configured: []string{"afile.txt"},
			want:       nil,
		},
		{
			name:       "a location may not escape the checkout",
			configured: []string{"../../etc"},
			want:       nil,
		},
		{
			name:       "a template may not escape the checkout either",
			configured: []string{"../outside/{{ .ResourceKind }}.json"},
			want:       nil,
		},
		{
			name:       "blank entries are ignored",
			configured: []string{"", "   "},
			want:       nil,
		},
		{
			name:       "an unusable location does not discard a usable one",
			configured: []string{"does/not/exist", ".github/schemas"},
			want:       []string{filepath.Join(repoDir, ".github/schemas")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveSchemaLocations(tc.configured, repoDir))
		})
	}
}

// Callers use this to decide whether to obtain a checkout at all, so it has to agree
// with what resolveSchemaLocations actually treats as relative.
func TestNeedsCheckout(t *testing.T) {
	tests := []struct {
		name      string
		locations []string
		want      bool
	}{
		{"no locations", nil, false},
		{"absolute only", []string{"/host/schemas"}, false},
		{"git url", []string{"git@github.com:org/schemas.git"}, false},
		{"http url", []string{"https://example.com/{{ .ResourceKind }}.json"}, false},
		{"blank entries", []string{"", "   "}, false},
		{"relative directory", []string{".github/schemas"}, true},
		{"relative template", []string{".github/schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json"}, true},
		{"relative among absolute", []string{"/host/schemas", ".github/schemas"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, NeedsCheckout(tc.locations))

			// whenever a checkout is said to be unnecessary, resolving without one must
			// not drop anything
			if !tc.want {
				withCheckout := resolveSchemaLocations(tc.locations, t.TempDir())
				assert.Equal(t, withCheckout, resolveSchemaLocations(tc.locations, ""))
			}
		})
	}
}

// Without a checkout a relative location cannot be resolved and is dropped; absolute and
// remote locations are unaffected, so the check still runs against those.
func TestResolveSchemaLocationsWithoutACheckout(t *testing.T) {
	assert.Nil(t, resolveSchemaLocations([]string{".github/schemas"}, ""))
	assert.Nil(t, resolveSchemaLocations(nil, t.TempDir()))
	assert.Equal(t, []string{"/host/schemas"}, resolveSchemaLocations([]string{"/host/schemas"}, ""))
}

// End to end against the real validator, using the layout openapi2jsonschema produces:
// flat files named <kind>_<version>.json. Nothing else resolves a custom kind, so the
// repository's schema is demonstrably the one doing the work.
func TestValidatesAgainstSchemasCommittedToTheRepository(t *testing.T) {
	repoDir := t.TempDir()
	schemaDir := filepath.Join(repoDir, ".github", "schemas")
	require.NoError(t, os.MkdirAll(schemaDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(schemaDir, "externalsecret_v1.json"), []byte(`{
	  "type": "object",
	  "properties": {
	    "spec": {
	      "type": "object",
	      "required": ["secretStoreRef"],
	      "properties": {
	        "secretStoreRef": {"type": "object"},
	        "refreshInterval": {"type": "string"}
	      }
	    }
	  }
	}`), 0o644))

	ctr := container.Container{Config: config.ServerConfig{
		SchemasLocations: []string{".github/schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json"},
	}}

	const header = "apiVersion: external-secrets.io/v1\nkind: ExternalSecret\nmetadata:\n  name: creds\n"

	t.Run("a valid resource passes", func(t *testing.T) {
		result, err := argoCdAppValidate(context.Background(), ctr, "app", "1.30.0", repoDir,
			[]string{header + "spec:\n  secretStoreRef:\n    name: store\n  refreshInterval: 1h\n"})
		require.NoError(t, err)
		assert.Equal(t, pkg.StateSuccess, result.State, result.Details)
	})

	t.Run("a missing required field is reported", func(t *testing.T) {
		result, err := argoCdAppValidate(context.Background(), ctr, "app", "1.30.0", repoDir,
			[]string{header + "spec:\n  refreshInterval: 1h\n"})
		require.NoError(t, err)
		assert.Equal(t, pkg.StateWarning, result.State)
		assert.Contains(t, result.Details, "Invalid")
	})

	t.Run("a wrongly typed field is reported", func(t *testing.T) {
		result, err := argoCdAppValidate(context.Background(), ctr, "app", "1.30.0", repoDir,
			[]string{header + "spec:\n  secretStoreRef:\n    name: store\n  refreshInterval: 3600\n"})
		require.NoError(t, err)
		assert.Equal(t, pkg.StateWarning, result.State)
		assert.Contains(t, result.Details, "Invalid")
	})
}

// kubeconform reports a missing schema without saying where it looked, so the report
// says it instead — otherwise the reader has nothing to act on, which is exactly the
// position an operator is in when a template or a filename does not line up.
func TestReportNamesTheLocationsSearchedForAMissingSchema(t *testing.T) {
	repoDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".github", "schemas"), 0o755))

	ctr := container.Container{Config: config.ServerConfig{
		SchemasLocations: []string{".github/schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json"},
	}}

	result, err := argoCdAppValidate(context.Background(), ctr, "app", "1.30.0", repoDir,
		[]string{"apiVersion: external-secrets.io/v1\nkind: ExternalSecret\nmetadata:\n  name: creds\nspec: {}\n"})
	require.NoError(t, err)

	assert.Equal(t, pkg.StateFailure, result.State)
	assert.Contains(t, result.Details, "could not find schema")
	assert.Contains(t, result.Details, "looked for in")
	// shown relative to the checkout, because the absolute form is a temporary clone
	assert.Contains(t, result.Details, ".github/schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json")
	assert.NotContains(t, result.Details, repoDir)
}

// The CRDs-catalog layout: <group>/<kind>_<version>.json, one directory per API group.
// This is what the catalog publishes and what openapi2jsonschema produces when run per
// group, so it is the layout a repository most often ends up with — and it needs
// .Group in the template, which is the full API group rather than its first label.
func TestValidatesAgainstTheGroupedCatalogLayout(t *testing.T) {
	repoDir := t.TempDir()
	for group, file := range map[string]string{
		"external-secrets.io":       "externalsecret_v1.json",
		"gateway.networking.k8s.io": "httproute_v1.json",
		"monitoring.googleapis.com": "podmonitoring_v1.json",
	} {
		dir := filepath.Join(repoDir, ".github", "schemas", group)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, file),
			[]byte(`{"type":"object","properties":{"spec":{"type":"object","required":["needed"],"properties":{"needed":{"type":"string"}}}}}`), 0o644))
	}

	ctr := container.Container{Config: config.ServerConfig{
		SchemasLocations: []string{".github/schemas/{{ .Group }}/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json"},
	}}

	for _, tc := range []struct{ apiVersion, kind string }{
		{"external-secrets.io/v1", "ExternalSecret"},
		{"gateway.networking.k8s.io/v1", "HTTPRoute"},
		{"monitoring.googleapis.com/v1", "PodMonitoring"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			header := "apiVersion: " + tc.apiVersion + "\nkind: " + tc.kind + "\nmetadata:\n  name: thing\n"

			result, err := argoCdAppValidate(context.Background(), ctr, "app", "1.30.0", repoDir,
				[]string{header + "spec:\n  needed: a-string\n"})
			require.NoError(t, err)
			assert.Equal(t, pkg.StateSuccess, result.State, result.Details)

			// and the schema is doing real work, not merely resolving
			result, err = argoCdAppValidate(context.Background(), ctr, "app", "1.30.0", repoDir,
				[]string{header + "spec: {}\n"})
			require.NoError(t, err)
			assert.Equal(t, pkg.StateWarning, result.State)
			assert.Contains(t, result.Details, "Invalid")
		})
	}
}

// A report with nothing missing stays as it was.
func TestReportOmitsLocationsWhenNothingIsMissing(t *testing.T) {
	repoDir := t.TempDir()
	schemaDir := filepath.Join(repoDir, ".github", "schemas")
	require.NoError(t, os.MkdirAll(schemaDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(schemaDir, "externalsecret_v1.json"),
		[]byte(`{"type":"object"}`), 0o644))

	ctr := container.Container{Config: config.ServerConfig{
		SchemasLocations: []string{".github/schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json"},
	}}

	result, err := argoCdAppValidate(context.Background(), ctr, "app", "1.30.0", repoDir,
		[]string{"apiVersion: external-secrets.io/v1\nkind: ExternalSecret\nmetadata:\n  name: creds\nspec: {}\n"})
	require.NoError(t, err)

	assert.Equal(t, pkg.StateSuccess, result.State, result.Details)
	assert.NotContains(t, result.Details, "looked for in")
}
