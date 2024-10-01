package kubeconform

import (
	"os"
	"strings"
	"testing"

	fixtures "github.com/go-git/go-git-fixtures/v4"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/zapier/kubechecks/pkg/container"
)

func TestDefaultGetSchemaLocations(t *testing.T) {
	ctr := container.Container{}
	schemaLocations := getSchemaLocations(ctr, "/some/other/path")

	// default schema location is "./schemas"
	assert.Len(t, schemaLocations, 1)
	assert.Equal(t, "default", schemaLocations[0])
}

func TestGetRemoteSchemaLocations(t *testing.T) {
	ctr := container.Container{}

	if os.Getenv("CI") == "" {
		t.Skip("Skipping testing. Only for CI environments")
	}

	basic := fixtures.Basic()
	fixture := basic.One()

	// t.Setenv("KUBECHECKS_SCHEMAS_LOCATION", fixture.URL)  // doesn't work because viper needs to initialize from root, which doesn't happen
	viper.Set("schemas-location", []string{fixture.URL})
	schemaLocations := getSchemaLocations(ctr, "/some/other/path")
	hasTmpDirPrefix := strings.HasPrefix(schemaLocations[0], "/tmp/schemas")
	assert.Truef(t, hasTmpDirPrefix, "invalid schemas location. Schema location should have prefix /tmp/schemas but has %s", schemaLocations[0])
}
