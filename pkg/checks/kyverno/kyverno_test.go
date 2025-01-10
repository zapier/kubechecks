package kyverno

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/kyverno/kyverno/api/kyverno"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	"github.com/kyverno/kyverno/cmd/cli/kubectl-kyverno/processor"
	engineapi "github.com/kyverno/kyverno/pkg/engine/api"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	apply "github.com/zapier/kubechecks/pkg/kyverno-kubectl"
)

var mockEngineResponse = engineapi.EngineResponse{
	Resource: unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "Pod",
			"apiVersion": "v1",
			"metadata":   map[string]interface{}{"namespace": "namespace1", "name": "mypod"},
		},
	},
	PolicyResponse: engineapi.PolicyResponse{
		Rules: []engineapi.RuleResponse{
			*engineapi.NewRuleResponse("Rule1", engineapi.Mutation, "failed due to reason X", engineapi.RuleStatusFail, nil),
		},
	},
}

var mockPolicy = kyvernov1.Policy{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "policy",
		Namespace: "abcd",
		Annotations: map[string]string{
			kyverno.AnnotationAutogenControllers: "all",
		},
	},
}

var mockKyvernoPolicy = engineapi.NewKyvernoPolicy(&mockPolicy)

func TestGetFailedRuleMsg(t *testing.T) {
	mockEngineResponse = mockEngineResponse.WithPolicy(mockKyvernoPolicy)
	tests := []struct {
		name           string
		applyResult    apply.Result
		expectedOutput string
	}{
		{
			name: "No failed rules",
			applyResult: apply.Result{
				PolicyRuleCount: 0,
				Resources:       []*unstructured.Unstructured{},
				RC: &processor.ResultCounts{
					Pass: 0, Fail: 0, Warn: 0, Error: 0, Skip: 0,
				},
				SkippedInvalidPolicies: apply.SkippedInvalidPolicies{
					Skipped: []string{},
					Invalid: []string{},
				},
				Responses: []engineapi.EngineResponse{},
			},
			expectedOutput: "",
		},
		{
			name: "Skipped and invalid policies",
			applyResult: apply.Result{
				SkippedInvalidPolicies: apply.SkippedInvalidPolicies{
					Skipped: []string{"policy1", "policy2"},
					Invalid: []string{"policy3"},
				},
			},
			expectedOutput: `
----------------------------------------------------------------------
Policies Skipped (as required variables are not provided by the user):
1. policy1
2. policy2

----------------------------------------------------------------------
Invalid Policies:
1. policy3

----------------------------------------------------------------------`,
		},
		{
			name: "Failed rules",
			applyResult: apply.Result{
				Responses: []engineapi.EngineResponse{
					mockEngineResponse,
				},
			},
			expectedOutput: `
policy ` + "`policy`" + ` -> resource ` + "`namespace1/Pod/mypod`" + ` failed: 

1 - Rule1 failed due to reason X 

----------------------------------------------------------------------
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := getFailedRuleMsg(tt.applyResult)
			assert.Equal(t, tt.expectedOutput, output)
		})
	}
}
func TestKyvernoValidate(t *testing.T) {
	tests := []struct {
		name                    string
		appName                 string
		targetKubernetesVersion string
		appManifests            []string
		expectedState           pkg.CommitState
		expectedSummary         string
		expectedDetails         string
		expectedError           bool
	}{
		{
			name:                    "Successful validation",
			appName:                 "test-app",
			targetKubernetesVersion: "1.21",
			appManifests: []string{`---
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
  labels:
    app.kubernetes.io/name: test
  spec:
    containers:
    - name: test-container
      image: test-image
`,
			},
			expectedState:   pkg.StateSuccess,
			expectedSummary: "<b>Show kyverno report:</b>",
			expectedDetails: `> Kyverno Policy Report

Applied 1 policy rule(s) to 1 resource(s)...



		pass: 1, fail: 0, warn: 0, error: 0, skip: 0`,
			expectedError: false,
		},
		{
			name:                    "Validation with failed rules",
			appName:                 "test-app",
			targetKubernetesVersion: "1.21",
			appManifests: []string{`---
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
  spec:
    containers:
    - name: test-container
      image: test-image
`,
			},
			expectedState:   pkg.StateWarning,
			expectedSummary: "<b>Show kyverno report:</b>",
			expectedDetails: `> Kyverno Policy Report

Applied 1 policy rule(s) to 1 resource(s)...


policy ` + "`require-labels`" + ` -> resource ` + "`default/Pod/test-pod`" + ` failed: 

1 - check-for-labels validation error: Either "metadata.labels" or "spec.template.metadata.labels" must include the labels: app.kubernetes.io/name. rule check-for-labels[0] failed at path /metadata/labels/ rule check-for-labels[1] failed at path /spec/ 

----------------------------------------------------------------------


		pass: 0, fail: 1, warn: 0, error: 0, skip: 0`,
			expectedError: false,
		},
	}

	testKyvernoPolicy := `---
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: require-labels
  annotations:
    policies.kyverno.io/title: Require app.kubernetes.io/name Labels
    policies.kyverno.io/category: Best Practices
    policies.kyverno.io/minversion: 1.6.0
    policies.kyverno.io/severity: medium
    policies.kyverno.io/subject: Deployment, DaemonSet, StatefulSet, Label
    policies.kyverno.io/description: >-
      Ensure that specific labels identifying semantic attributes are applied.
      This policy validates that the labels "app.kubernetes.io/name" is present
spec:
  background: true
  emitWarning: true
  rules:
  - name: check-for-labels
    match:
      any:
      - resources:
          kinds:
          - Pod
          - Deployment
          - StatefulSet
          - DaemonSet
    skipBackgroundRequests: true
    validate:
      failureAction: Audit
      allowExistingViolations: true
      message: >-
        Either "metadata.labels" or "spec.template.metadata.labels" must include the labels:
        app.kubernetes.io/name
      anyPattern:
      - metadata:
          labels:
            app.kubernetes.io/name: "?*"
      - spec:
          template:
            metadata:
              labels:
                app.kubernetes.io/name: "?*"
`

	fmt.Println("Creating temporary policy file")
	policyTempFile, err := os.CreateTemp(os.TempDir(), "kyverno-policy-*.yaml")
	if err != nil {
		t.Error("Failed to create temporary file", err)
		t.FailNow()
	}

	fmt.Println("temporary policy file created at", policyTempFile.Name())
	// defer os.Remove(policyTempFile.Name())

	if _, err := policyTempFile.WriteString(testKyvernoPolicy); err != nil {
		t.Error("Failed to write policy to file", err)
		t.FailNow()
	}
	if err := policyTempFile.Close(); err != nil {
		t.Error("Failed to close temp policy file", err)
		t.FailNow()
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctr := container.Container{
				Config: config.ServerConfig{
					KyvernoPoliciesLocation: []string{policyTempFile.Name()},
				},
			}

			result, err := kyvernoValidate(context.Background(), ctr, tt.appName, tt.targetKubernetesVersion, tt.appManifests)
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedState, result.State)
			assert.Equal(t, tt.expectedSummary, result.Summary)
			assert.Equal(t, tt.expectedDetails, result.Details)
		})
	}
}
