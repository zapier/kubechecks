package validate

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	fixtures "github.com/go-git/go-git-fixtures/v4"
	"github.com/spf13/viper"

	"github.com/stretchr/testify/assert"
)

func TestDefaultGetSchemaLocations(t *testing.T) {
	getSchemasOnce = *new(sync.Once)
	schemaLocations := getSchemaLocations()

	// default schema location is "./schemas"
	assert.Equal(t, "./schemas/{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json", schemaLocations[0])
}

func TestGetRemoteSchemaLocations(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("Skipping testing. Only for CI environments")
	}

	getSchemasOnce = *new(sync.Once)

	fixture := fixtures.Basic().One()
	fmt.Println(fixture.URL)

	// t.Setenv("KUBECHECKS_SCHEMAS_LOCATION", fixture.URL)  // doesn't work because viper needs to initialize from root, which doesn't happen
	viper.Set("schemas-location", fixture.URL)
	schemaLocations := getSchemaLocations()
	hasTmpDirPrefix := strings.HasPrefix(schemaLocations[0], "/tmp/schemas")
	assert.Equal(t, hasTmpDirPrefix, true, "invalid schemas location. Schema location should have prefix /tmp/schemas but has %s", schemaLocations[0])
}
