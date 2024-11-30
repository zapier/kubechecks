package diff

import (
	"encoding/json"
	"testing"

	argoappv1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	"github.com/stretchr/testify/assert"
)

func TestIsApp(t *testing.T) {
	tests := []struct {
		name      string
		item      objKeyLiveTarget
		manifests []byte
		expected  bool
	}{
		{
			name: "Valid Application",
			item: objKeyLiveTarget{
				key: kube.ResourceKey{
					Group: "argoproj.io",
					Kind:  "Application",
				},
			},
			manifests: func() []byte {
				app := argoappv1.Application{
					Spec: argoappv1.ApplicationSpec{
						Project: "default",
					},
				}
				data, _ := json.Marshal(app)
				return data
			}(),
			expected: true,
		},
		{
			name: "Invalid Group",
			item: objKeyLiveTarget{
				key: kube.ResourceKey{
					Group: "invalid.group",
					Kind:  "Application",
				},
			},
			manifests: []byte{},
			expected:  false,
		},
		{
			name: "Invalid Kind",
			item: objKeyLiveTarget{
				key: kube.ResourceKey{
					Group: "argoproj.io",
					Kind:  "InvalidKind",
				},
			},
			manifests: []byte{},
			expected:  false,
		},
		{
			name: "Invalid JSON",
			item: objKeyLiveTarget{
				key: kube.ResourceKey{
					Group: "argoproj.io",
					Kind:  "Application",
				},
			},
			manifests: []byte("invalid json"),
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, result := isApp(tt.item, tt.manifests)
			assert.Equal(t, tt.expected, result)
		})
	}
}
