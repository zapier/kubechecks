package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffTool(t *testing.T) {
	tests := []struct {
		name         string
		diff         string
		wantContains []string
		wantMissing  []string
		wantExact    string
	}{
		{
			name:         "returns diff content",
			diff:         "===== apps/Deployment default/web ======\n-replicas: 2\n+replicas: 5",
			wantContains: []string{"replicas: 5"},
		},
		{
			name:      "empty diff",
			diff:      "",
			wantExact: "No changes detected.",
		},
		{
			name:         "filters CRDs",
			diff:         "===== apiextensions.k8s.io/CustomResourceDefinition /mycrd ======\n+some crd stuff\n===== apps/Deployment default/web ======\n-replicas: 2\n+replicas: 5",
			wantContains: []string{"replicas: 5"},
			wantMissing:  []string{"CustomResourceDefinition", "crd stuff"},
		},
		{
			name:         "filters Secrets",
			diff:         "===== /Secret default/my-secret ======\n+data: sensitive\n===== apps/Deployment default/web ======\n-replicas: 2\n+replicas: 5",
			wantContains: []string{"replicas: 5"},
			wantMissing:  []string{"sensitive", "Secret"},
		},
		{
			name:         "does not filter SecretProviderClass",
			diff:         "===== secrets-store.csi.x-k8s.io/SecretProviderClass default/my-spc ======\n+provider: aws",
			wantContains: []string{"SecretProviderClass", "provider: aws"},
		},
		{
			name:      "only excluded kinds returns empty message",
			diff:      "===== /Secret default/my-secret ======\n+data: sensitive",
			wantExact: "No changes detected (CRDs and Secrets excluded).",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := DiffTool(tt.diff)
			assert.Equal(t, "get_diff", tool.Def.Name)

			result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
			require.NoError(t, err)

			if tt.wantExact != "" {
				assert.Equal(t, tt.wantExact, result)
				return
			}
			for _, s := range tt.wantContains {
				assert.Contains(t, result, s)
			}
			for _, s := range tt.wantMissing {
				assert.NotContains(t, result, s)
			}
		})
	}
}

func TestRenderedManifestsTool(t *testing.T) {
	tests := []struct {
		name         string
		manifests    []string
		wantContains []string
		wantMissing  []string
		wantExact    string
	}{
		{
			name: "returns manifests",
			manifests: []string{
				"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web",
				"apiVersion: v1\nkind: Service\nmetadata:\n  name: web",
			},
			wantContains: []string{"Deployment", "Service", "---"},
		},
		{
			name:      "nil manifests",
			manifests: nil,
			wantExact: "No manifests available (CRDs and Secrets excluded).",
		},
		{
			name: "filters CRDs",
			manifests: []string{
				"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web",
				"apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: mycrd",
			},
			wantContains: []string{"Deployment"},
			wantMissing:  []string{"CustomResourceDefinition"},
		},
		{
			name: "filters Secrets",
			manifests: []string{
				"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web",
				"apiVersion: v1\nkind: Secret\nmetadata:\n  name: my-secret\ndata:\n  password: c2VjcmV0",
			},
			wantContains: []string{"Deployment"},
			wantMissing:  []string{"Secret", "c2VjcmV0"},
		},
		{
			name: "truncates large manifests",
			manifests: []string{
				strings.Repeat("a", maxManifestBytes+1000),
			},
			wantContains: []string{"[truncated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := RenderedManifestsTool(tt.manifests)
			assert.Equal(t, "get_rendered_manifests", tool.Def.Name)

			result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
			require.NoError(t, err)

			if tt.wantExact != "" {
				assert.Equal(t, tt.wantExact, result)
				return
			}
			for _, s := range tt.wantContains {
				assert.Contains(t, result, s)
			}
			for _, s := range tt.wantMissing {
				assert.NotContains(t, result, s)
			}
			if tt.name == "truncates large manifests" {
				assert.LessOrEqual(t, len(result), maxManifestBytes+100)
			}
		})
	}
}

func TestAppInfoTool(t *testing.T) {
	tool := AppInfoTool("web-app", "production", "prod-cluster", "default", "repo=https://github.com/org/repo, path=charts/web")
	assert.Equal(t, "get_app_info", tool.Def.Name)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Contains(t, result, "web-app")
	assert.Contains(t, result, "production")
	assert.Contains(t, result, "prod-cluster")
}

func TestExtractKindFromDiffHeader(t *testing.T) {
	tests := []struct {
		name    string
		section string
		want    string
	}{
		{
			name:    "core Secret",
			section: "/Secret default/my-secret ======\n+data: foo",
			want:    "Secret",
		},
		{
			name:    "CRD with group",
			section: "apiextensions.k8s.io/CustomResourceDefinition /mycrd ======\n+spec: foo",
			want:    "CustomResourceDefinition",
		},
		{
			name:    "apps Deployment",
			section: "apps/Deployment default/web ======\n-replicas: 2",
			want:    "Deployment",
		},
		{
			name:    "SecretProviderClass",
			section: "secrets-store.csi.x-k8s.io/SecretProviderClass default/my-spc ======\n+provider: aws",
			want:    "SecretProviderClass",
		},
		{
			name:    "empty section",
			section: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractKindFromDiffHeader(tt.section))
		})
	}
}
