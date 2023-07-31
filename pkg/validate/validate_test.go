package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetSchemaLocations(t *testing.T) {
	schemaLocations := getSchemaLocations()

	// default schema location is "./schemas"
	assert.Equal(t, "./schemas/{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json", schemaLocations[0])
}
