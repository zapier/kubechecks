package rego

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs/gitlab_client"
)

func mustWrite(t *testing.T, filePath, content string) {
	err := os.WriteFile(filePath, []byte(content), 0o666)
	require.NoError(t, err)
}

type yamlMap map[string]interface{}

func TestHappyPath(t *testing.T) {
	t.Parallel()

	testcases := []struct {
		name, policy string
		manifest     yamlMap
		expected     pkg.CommitState
	}{
		{
			name: "good policy, good manifest",
			policy: `package tests

deny[msg] {
  input.kind == "Deployment"
  not input.spec.template.spec.securityContext.runAsNonRoot

  msg := "Containers must not run as root"
}
`,
			manifest: yamlMap{
				"kind": "Deployment",
				"metadata": yamlMap{
					"name":      "test-deployment",
					"namespace": "test-namespace",
				},
				"spec": yamlMap{
					"template": yamlMap{
						"spec": yamlMap{
							"securityContext": yamlMap{
								"runAsNonRoot": true,
							},
						},
					},
				},
			},
			expected: pkg.StateSuccess,
		},
		{
			name: "good policy, bad manifest",
			policy: `package tests

deny[msg] {
  input.kind == "Deployment"
  not input.spec.template.spec.securityContext.runAsNonRoot

  msg := "Containers must not run as root"
}
`,
			manifest: yamlMap{
				"kind": "Deployment",
				"metadata": yamlMap{
					"name":      "test-deployment",
					"namespace": "test-namespace",
				},
				"spec": yamlMap{
					"template": yamlMap{
						"spec": yamlMap{
							"securityContext": yamlMap{
								"runAsNonRoot": false,
							},
						},
					},
				},
			},
			expected: pkg.StateFailure,
		},
		{
			name: "good policy, missing key manifest",
			policy: `package tests

deny[msg] {
  input.kind == "Deployment"
  not input.spec.template.spec.securityContext.runAsNonRoot

  msg := "Containers must not run as root"
}
`,
			manifest: yamlMap{
				"kind": "Deployment",
				"metadata": yamlMap{
					"name":      "test-deployment",
					"namespace": "test-namespace",
				},
				"spec": yamlMap{
					"template": yamlMap{
						"spec": yamlMap{
							"securityContext": yamlMap{},
						},
					},
				},
			},
			expected: pkg.StateFailure,
		},
		{
			name: "warn policy, bad manifest",
			policy: `package tests

warn[msg] {
  input.kind == "Deployment"
  not input.spec.template.spec.securityContext.runAsNonRoot

  msg := "Containers should not run as root"
}
`,
			manifest: yamlMap{
				"kind": "Deployment",
				"metadata": yamlMap{
					"name":      "test-deployment",
					"namespace": "test-namespace",
				},
				"spec": yamlMap{
					"template": yamlMap{
						"spec": yamlMap{
							"securityContext": yamlMap{
								"runAsNonRoot": false,
							},
						},
					},
				},
			},
			expected: pkg.StateWarning,
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			policiesPath, err := os.MkdirTemp("", "kubechecks-test-policies-")
			require.NoError(t, err)

			mustWrite(t, filepath.Join(policiesPath, "policy.rego"), tc.policy)

			cfg := config.ServerConfig{
				ShowDebugInfo: true,

				PoliciesLocation: []string{policiesPath},
			}
			c, err := NewChecker(cfg)
			require.NoError(t, err)

			manifestBytes, err := yaml.Marshal(tc.manifest)
			require.NoError(t, err)

			ctx := context.TODO()
			request := checks.Request{
				Container: container.Container{
					Config:    cfg,
					VcsClient: new(gitlab_client.Client),
				},
				YamlManifests: []string{string(manifestBytes)},
			}
			cr, err := c.Check(ctx, request)
			require.NoError(t, err)

			assert.Equal(t, tc.expected, cr.State, "%s\n\n%s", cr.Summary, cr.Details)
		})
	}
}
