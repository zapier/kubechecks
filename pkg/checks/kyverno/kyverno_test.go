package kyverno

import (
	"testing"

	"github.com/kyverno/kyverno/api/kyverno"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	"github.com/kyverno/kyverno/cmd/cli/kubectl-kyverno/processor"
	engineapi "github.com/kyverno/kyverno/pkg/engine/api"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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
