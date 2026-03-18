package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/zapier/kubechecks/pkg/aireview"
	client "github.com/zapier/kubechecks/pkg/kubernetes"
)

// --- Schemas for local cluster tools (no cluster parameter) ---

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
	"properties": {}
}`)

// --- Schemas for remote cluster tools (cluster parameter required) ---

var queryRemoteKubernetesSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"cluster": {
			"type": "string",
			"description": "Cluster name as registered in ArgoCD. Use list_clusters to discover available clusters."
		},
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
	"required": ["cluster", "namespace", "resource"]
}`)

var listRemoteNamespacesSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"cluster": {
			"type": "string",
			"description": "Cluster name as registered in ArgoCD. Use list_clusters to discover available clusters."
		}
	},
	"required": ["cluster"]
}`)

var listClustersSchema = json.RawMessage(`{
	"type": "object",
	"properties": {}
}`)

// --- Local cluster tools (always available) ---

// KubernetesQueryTool returns a tool that queries resources on the local/management cluster.
func KubernetesQueryTool(dynamicClient dynamic.Interface, discoveryClient discovery.DiscoveryInterface) aireview.Tool {
	return aireview.NewTool(
		"query_kubernetes",
		"Query Kubernetes resources on the management cluster. Returns JSON. Use to check current state of deployments, HPAs, KEDA ScaledObjects, services, ingresses, configmaps, etc. For CRD resources, use 'resource.group' format (e.g. 'scaledobjects.keda.sh').",
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
			return queryResources(ctx, dynamicClient, discoveryClient, params.Namespace, params.Resource, params.Name, params.LabelSelector)
		},
	)
}

// ListNamespacesTool returns a tool that lists namespaces on the local/management cluster.
func ListNamespacesTool(clientset kubernetes.Interface) aireview.Tool {
	return aireview.NewTool(
		"list_namespaces",
		"List all namespaces on the management cluster.",
		listNamespacesSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			return listNamespaces(ctx, clientset)
		},
	)
}

// --- Remote cluster tools (only added when multi-cluster is configured) ---

// RemoteKubernetesQueryTool returns a tool that queries resources on any ArgoCD-registered cluster.
func RemoteKubernetesQueryTool(mcm *client.MultiClusterManager) aireview.Tool {
	return aireview.NewTool(
		"query_remote_kubernetes",
		"Query Kubernetes resources on a remote cluster registered in ArgoCD. Use list_clusters to discover available clusters. Returns JSON.",
		queryRemoteKubernetesSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Cluster       string `json:"cluster"`
				Namespace     string `json:"namespace"`
				Resource      string `json:"resource"`
				Name          string `json:"name"`
				LabelSelector string `json:"label_selector"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			clients, err := mcm.GetClusterClients(ctx, params.Cluster)
			if err != nil {
				return fmt.Sprintf("Failed to connect to cluster %q: %s", params.Cluster, err), nil
			}
			return queryResources(ctx, clients.DynamicClient, clients.Clientset.Discovery(), params.Namespace, params.Resource, params.Name, params.LabelSelector)
		},
	)
}

// RemoteListNamespacesTool returns a tool that lists namespaces on a remote cluster.
func RemoteListNamespacesTool(mcm *client.MultiClusterManager) aireview.Tool {
	return aireview.NewTool(
		"list_remote_namespaces",
		"List all namespaces on a remote cluster registered in ArgoCD. Use list_clusters to discover available clusters.",
		listRemoteNamespacesSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Cluster string `json:"cluster"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			clients, err := mcm.GetClusterClients(ctx, params.Cluster)
			if err != nil {
				return fmt.Sprintf("Failed to connect to cluster %q: %s", params.Cluster, err), nil
			}
			return listNamespaces(ctx, clients.Clientset)
		},
	)
}

// ListClustersTool returns a tool that lists all clusters registered in ArgoCD.
func ListClustersTool(mcm *client.MultiClusterManager) aireview.Tool {
	return aireview.NewTool(
		"list_clusters",
		"List all Kubernetes clusters registered in ArgoCD. Returns cluster names and server URLs. Use the cluster name with query_remote_kubernetes and list_remote_namespaces to query remote clusters.",
		listClustersSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			clusters, err := mcm.ListClusters(ctx)
			if err != nil {
				return fmt.Sprintf("Failed to list clusters: %s", err), nil
			}
			b, _ := json.Marshal(clusters)
			return string(b), nil
		},
	)
}

// --- Shared helpers ---

func queryResources(ctx context.Context, dynClient dynamic.Interface, discClient discovery.DiscoveryInterface, namespace, resource, name, labelSelector string) (string, error) {
	gvr, err := resolveGVR(discClient, resource)
	if err != nil {
		return fmt.Sprintf("Resource type %q is not available in this cluster.", resource), nil
	}

	if name != "" {
		obj, err := dynClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return handleK8sError(err, resource, name, namespace), nil
		}
		return marshalClean(obj.Object)
	}

	listOpts := metav1.ListOptions{}
	if labelSelector != "" {
		listOpts.LabelSelector = labelSelector
	}
	list, err := dynClient.Resource(gvr).Namespace(namespace).List(ctx, listOpts)
	if err != nil {
		return handleK8sListError(err, resource, namespace), nil
	}

	var items []map[string]any
	for _, item := range list.Items {
		items = append(items, cleanObject(item.Object))
	}
	return marshalCompact(items)
}

func listNamespaces(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Sprintf("Failed to list namespaces: %s", err), nil
	}
	var names []string
	for _, ns := range nsList.Items {
		names = append(names, ns.Name)
	}
	b, _ := json.Marshal(names)
	return string(b), nil
}

func handleK8sError(err error, resource, name, namespace string) string {
	if apierrors.IsNotFound(err) {
		return fmt.Sprintf("Resource %s/%s not found in namespace %q.", resource, name, namespace)
	}
	if apierrors.IsForbidden(err) {
		return fmt.Sprintf("Access denied for %s/%s in namespace %q. The service account lacks RBAC permissions.", resource, name, namespace)
	}
	return fmt.Sprintf("Error getting %s/%s: %s", resource, name, err)
}

func handleK8sListError(err error, resource, namespace string) string {
	if apierrors.IsNotFound(err) {
		return fmt.Sprintf("Resource type %q is not available in this cluster.", resource)
	}
	if apierrors.IsForbidden(err) {
		return fmt.Sprintf("Access denied for listing %s in namespace %q. The service account lacks RBAC permissions.", resource, namespace)
	}
	return fmt.Sprintf("Error listing %s: %s", resource, err)
}

func resolveGVR(discoveryClient discovery.DiscoveryInterface, resource string) (schema.GroupVersionResource, error) {
	group := ""
	resourceName := resource
	if parts := strings.SplitN(resource, ".", 2); len(parts) == 2 {
		resourceName = parts[0]
		group = parts[1]
	}

	apiResourceLists, err := discoveryClient.ServerPreferredResources()
	if err != nil {
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

	return schema.GroupVersionResource{
		Group:    group,
		Version:  "v1",
		Resource: resourceName,
	}, nil
}

func cleanObject(obj map[string]any) map[string]any {
	if metadata, ok := obj["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
		if annotations, ok := metadata["annotations"].(map[string]any); ok {
			delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		}
	}
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
	s := string(b)
	if len(s) > maxManifestBytes {
		s = s[:maxManifestBytes] + "\n\n[truncated — response exceeded size limit]"
	}
	return s, nil
}
