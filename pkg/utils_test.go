package pkg

import (
	"testing"
)

func TestGetMessageHeader(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
		want       string
	}{
		{
			name:       "empty identifier",
			identifier: "",
			want:       "## Kubechecks  Report\n",
		},
		{
			name:       "with identifier",
			identifier: "test-instance",
			want:       "## Kubechecks test-instance Report\n",
		},
		{
			name:       "with special characters",
			identifier: "test-instance-123!@#",
			want:       "## Kubechecks test-instance-123!@# Report\n",
		},
		{
			name:       "with spaces",
			identifier: "test instance",
			want:       "## Kubechecks test instance Report\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetMessageHeader(tt.identifier)
			if got != tt.want {
				t.Errorf("GetMessageHeader() = %q, want %q", got, tt.want)
			}
		})
	}
}
