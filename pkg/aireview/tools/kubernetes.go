package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/zapier/kubechecks/pkg/aireview"
)

var queryKubernetesSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"namespace": {
			"type": "string",
			"description": "Kubernetes namespace to query"
		},
		"resource": {
			"type": "string",
			"description": "Resource type to query, e.g. 'deployments', 'services', 'horizontalpodautoscalers', 'scaledobjects.keda.sh'. Use plural form. For CRDs, use 'resource.group' format"
		},
		"name": {
			"type": "string",
			"description": "Optional: specific resource name. Omit to list all resources of this type"
		},
		"label_selector": {
			"type": "string",
			"description": "Optional: label selector to filter resources, e.g. 'app=web'"
		}
	},
	"required": ["namespace", "resource"]
}`)

var listNamespacesSchema = json.RawMessage(`{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`)

// KubernetesQueryTool returns a tool that queries Kubernetes resources.
// It uses a dynamic client so it can query any resource type including CRDs.
func KubernetesQueryTool(dynamicClient dynamic.Interface, discoveryClient discovery.DiscoveryInterface) aireview.Tool {
	return aireview.NewTool(
		"query_kubernetes",
		"Query Kubernetes resources in the cluster. Returns JSON. Use to check current state of deployments, HPAs, KEDA ScaledObjects, services, ingresses, configmaps, etc. For CRD resources, use 'resource.group' format (e.g. 'scaledobjects.keda.sh').",
		queryKubernetesSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Namespace     string `json:"namespace"`
				Resource      string `json:"resource"`
				Name          string `json:"name"`
				LabelSelector string `json:"label_selector"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			gvr, err := resolveGVR(discoveryClient, params.Resource)
			if err != nil {
				return "", fmt.Errorf("failed to resolve resource %q: %w", params.Resource, err)
			}

			if params.Name != "" {
				obj, err := dynamicClient.Resource(gvr).Namespace(params.Namespace).Get(ctx, params.Name, metav1.GetOptions{})
				if err != nil {
					return "", fmt.Errorf("failed to get %s/%s: %w", params.Resource, params.Name, err)
				}
				return marshalClean(obj.Object)
			}

			listOpts := metav1.ListOptions{}
			if params.LabelSelector != "" {
				listOpts.LabelSelector = params.LabelSelector
			}
			list, err := dynamicClient.Resource(gvr).Namespace(params.Namespace).List(ctx, listOpts)
			if err != nil {
				return "", fmt.Errorf("failed to list %s: %w", params.Resource, err)
			}

			// Return compact summary to save tokens
			var items []map[string]any
			for _, item := range list.Items {
				items = append(items, cleanObject(item.Object))
			}
			return marshalCompact(items)
		},
	)
}

// ListNamespacesTool returns a tool that lists all namespaces in the cluster.
func ListNamespacesTool(clientset kubernetes.Interface) aireview.Tool {
	return aireview.NewTool(
		"list_namespaces",
		"List all namespaces in the Kubernetes cluster.",
		listNamespacesSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			if err != nil {
				return "", fmt.Errorf("failed to list namespaces: %w", err)
			}
			var names []string
			for _, ns := range nsList.Items {
				names = append(names, ns.Name)
			}
			b, _ := json.Marshal(names)
			return string(b), nil
		},
	)
}

// resolveGVR resolves a resource string to a GroupVersionResource.
// Accepts formats like "deployments", "horizontalpodautoscalers", "scaledobjects.keda.sh".
func resolveGVR(discoveryClient discovery.DiscoveryInterface, resource string) (schema.GroupVersionResource, error) {
	// Check if resource has a group suffix (e.g., "scaledobjects.keda.sh")
	group := ""
	resourceName := resource
	if parts := strings.SplitN(resource, ".", 2); len(parts) == 2 {
		resourceName = parts[0]
		group = parts[1]
	}

	// Use discovery to find the exact GVR
	apiResourceLists, err := discoveryClient.ServerPreferredResources()
	if err != nil {
		// Partial results are OK — discovery can return errors for some groups
		if apiResourceLists == nil {
			return schema.GroupVersionResource{}, fmt.Errorf("discovery failed: %w", err)
		}
	}

	for _, apiResourceList := range apiResourceLists {
		gv, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if err != nil {
			continue
		}
		if group != "" && gv.Group != group {
			continue
		}
		for _, apiResource := range apiResourceList.APIResources {
			if apiResource.Name == resourceName {
				return schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: resourceName,
				}, nil
			}
		}
	}

	// Fallback: assume core group with v1
	return schema.GroupVersionResource{
		Group:    group,
		Version:  "v1",
		Resource: resourceName,
	}, nil
}

// cleanObject removes noisy fields from a Kubernetes object to reduce token usage.
func cleanObject(obj map[string]any) map[string]any {
	// Remove managedFields and last-applied-configuration
	if metadata, ok := obj["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
		if annotations, ok := metadata["annotations"].(map[string]any); ok {
			delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		}
	}
	// Remove status.conditions details if too verbose — keep the rest of status
	return obj
}

func marshalClean(obj map[string]any) (string, error) {
	cleaned := cleanObject(obj)
	return marshalCompact(cleaned)
}

func marshalCompact(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	// Truncate if too large
	s := string(b)
	if len(s) > maxManifestBytes {
		s = s[:maxManifestBytes] + "\n\n[truncated — response exceeded size limit]"
	}
	return s, nil
}
