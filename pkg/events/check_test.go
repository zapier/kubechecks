package events

import (
	"errors"
	"fmt"
	"testing"
)

// TestCleanupGetManifestsError tests the cleanupGetManifestsError function.
func TestCleanupGetManifestsError(t *testing.T) {
	checkEvent := &CheckEvent{TempWorkingDir: "/tmp/work"}

	tests := []struct {
		name          string
		inputErr      error
		expectedError string
	}{
		{
			name:          "helm error",
			inputErr:      errors.New("`helm template . --name-template kubechecks --namespace kubechecks --kube-version 1.22 --values /tmp/kubechecks-mr-clone2267947074/manifests/tooling-eks-01/values.yaml --values /tmp/kubechecks-mr-clone2267947074/manifests/tooling-eks-01/current-tag.yaml --api-versions storage.k8s.io/v1 --api-versions storage.k8s.io/v1beta1 --api-versions v1 --api-versions vault.banzaicloud.com/v1alpha1 --api-versions velero.io/v1 --api-versions vpcresources.k8s.aws/v1beta1 --include-crds` failed exit status 1: Error: execution error at (kubechecks/charts/web/charts/ingress/templates/ingress.yaml:7:20): ingressClass value is required\\n\\nUse --debug flag to render out invalid YAML"),
			expectedError: "Helm Error: execution error at (kubechecks/charts/web/charts/ingress/templates/ingress.yaml:7:20): ingressClass value is required\\n\\nUse --debug flag to render out invalid YAML",
		},
		{
			name:          "strip temp directory",
			inputErr:      fmt.Errorf("Error: %s/tmpfile.yaml not found", checkEvent.TempWorkingDir),
			expectedError: "Error: tmpfile.yaml not found",
		},
		{
			name:          "strip temp directory and helm error",
			inputErr:      fmt.Errorf("`helm template . --name-template in-cluster-echo-server --namespace echo-server --kube-version 1.25 --values %s/apps/echo-server/in-cluster/values.yaml --values %s/apps/echo-server/in-cluster/notexist.yaml --api-versions admissionregistration.k8s.io/v1 --api-versions admissionregistration.k8s.io/v1/MutatingWebhookConfiguration --api-versions v1/Secret --api-versions v1/Service --api-versions v1/ServiceAccount --include-crds` failed exit status 1: Error: open %s/apps/echo-server/in-cluster/notexist.yaml: no such file or directory", checkEvent.TempWorkingDir, checkEvent.TempWorkingDir, checkEvent.TempWorkingDir),
			expectedError: "Helm Error: open apps/echo-server/in-cluster/notexist.yaml: no such file or directory",
		},
		{
			name:          "other error",
			inputErr:      errors.New("Error: unknown error"),
			expectedError: "Error: unknown error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanedError := checkEvent.cleanupGetManifestsError(tt.inputErr)
			if cleanedError != tt.expectedError {
				t.Errorf("Expected error: %s, \n                    Received: %s", tt.expectedError, cleanedError)
			}
		})
	}
}
