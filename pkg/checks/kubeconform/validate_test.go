package kubeconform

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zapier/kubechecks/pkg/container"
)

func TestDefaultGetSchemaLocations(t *testing.T) {
	ctr := container.Container{}
	schemaLocations := getSchemaLocations(ctr)

	// default schema location is "./schemas"
	assert.Len(t, schemaLocations, 1)
	assert.Equal(t, "default", schemaLocations[0])
}
