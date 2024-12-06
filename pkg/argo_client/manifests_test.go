package argo_client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAreSameTargetRef(t *testing.T) {
	testcases := map[string]struct {
		ref1, ref2 string
		expected   bool
	}{
		"same":      {"one", "one", true},
		"different": {"one", "two", false},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := areSameTargetRef(tc.ref1, tc.ref2)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestSplitRefFromPath(t *testing.T) {
	testcases := map[string]struct {
		input         string
		refName, path string
		err           error
	}{
		"simple": {
			"$values/charts/prometheus/values.yaml", "values", "charts/prometheus/values.yaml", nil,
		},
		"too-short": {
			"$values", "", "", ErrInvalidSourceRef,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			ref, path, err := splitRefFromPath(tc.input)
			if tc.err != nil {
				assert.EqualError(t, err, tc.err.Error())
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tc.refName, ref)
			assert.Equal(t, tc.path, path)
		})
	}
}
