package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/argoproj/argo-cd/v3/pkg/apiclient/application"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/aireview"
	"github.com/zapier/kubechecks/pkg/argo_client"
)

var queryAppResourcesSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"app_name": {
			"type": "string",
			"description": "ArgoCD application name (e.g. 'staging-eks-01-web'). Use the application name from the review context."
		}
	},
	"required": ["app_name"]
}`)

var getAppResourceSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"app_name": {
			"type": "string",
			"description": "ArgoCD application name"
		},
		"group": {
			"type": "string",
			"description": "API group (e.g. 'apps' for Deployments, '' for Services/ConfigMaps, 'autoscaling' for HPAs, 'keda.sh' for ScaledObjects)"
		},
		"kind": {
			"type": "string",
			"description": "Resource kind (e.g. 'Deployment', 'Service', 'HorizontalPodAutoscaler', 'ScaledObject')"
		},
		"name": {
			"type": "string",
			"description": "Resource name"
		},
		"namespace": {
			"type": "string",
			"description": "Resource namespace"
		}
	},
	"required": ["app_name", "kind", "name", "namespace"]
}`)

// QueryAppResourcesTool returns a tool that lists all managed resources for an ArgoCD application.
// Uses ArgoCD's ManagedResources API — works across all clusters ArgoCD manages without direct K8s access.
func QueryAppResourcesTool(argoClient *argo_client.ArgoClient) aireview.Tool {
	return aireview.NewTool(
		"query_app_resources",
		"List all Kubernetes resources managed by an ArgoCD application. Returns the live state of deployments, services, HPAs, configmaps, etc. across any cluster ArgoCD manages. Use this to check current state before assessing impact of changes.",
		queryAppResourcesSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				AppName string `json:"app_name"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			closer, appClient := argoClient.GetApplicationClient()
			defer pkg.WithErrorLogging(closer.Close, "failed to close application connection")

			resources, err := appClient.ManagedResources(ctx, &application.ResourcesQuery{
				ApplicationName: &params.AppName,
			})
			if err != nil {
				return fmt.Sprintf("Failed to get resources for app %q: %s", params.AppName, err), nil
			}

			if resources == nil || len(resources.Items) == 0 {
				return fmt.Sprintf("No managed resources found for app %q (app may be new or not yet synced).", params.AppName), nil
			}

			// Build a summary of all resources with their live state
			var items []map[string]any
			for _, r := range resources.Items {
				item := map[string]any{
					"group":     r.Group,
					"kind":      r.Kind,
					"namespace": r.Namespace,
					"name":      r.Name,
				}

				// Include live state if available
				if r.LiveState != "" {
					var liveObj map[string]any
					if err := json.Unmarshal([]byte(r.LiveState), &liveObj); err == nil {
						cleanObject(liveObj)
						item["live"] = liveObj
					}
				}

				items = append(items, item)
			}

			return marshalCompact(items)
		},
	)
}

// GetAppResourceTool returns a tool that gets the live state of a specific resource in an ArgoCD application.
func GetAppResourceTool(argoClient *argo_client.ArgoClient) aireview.Tool {
	return aireview.NewTool(
		"get_app_resource",
		"Get the full live state of a specific Kubernetes resource managed by an ArgoCD application. Use for detailed inspection of a deployment's replicas, resource limits, HPA config, etc.",
		getAppResourceSchema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				AppName   string `json:"app_name"`
				Group     string `json:"group"`
				Kind      string `json:"kind"`
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			closer, appClient := argoClient.GetApplicationClient()
			defer pkg.WithErrorLogging(closer.Close, "failed to close application connection")

			resources, err := appClient.ManagedResources(ctx, &application.ResourcesQuery{
				ApplicationName: &params.AppName,
			})
			if err != nil {
				return fmt.Sprintf("Failed to get resources for app %q: %s", params.AppName, err), nil
			}

			if resources == nil {
				return fmt.Sprintf("No resources found for app %q.", params.AppName), nil
			}

			// Find the matching resource
			for _, r := range resources.Items {
				kindMatch := strings.EqualFold(r.Kind, params.Kind)
				groupMatch := params.Group == "" || r.Group == params.Group
				nameMatch := r.Name == params.Name
				nsMatch := r.Namespace == params.Namespace

				if kindMatch && groupMatch && nameMatch && nsMatch {
					if r.LiveState == "" {
						return fmt.Sprintf("Resource %s/%s found but has no live state (not yet deployed).", params.Kind, params.Name), nil
					}

					var liveObj map[string]any
					if err := json.Unmarshal([]byte(r.LiveState), &liveObj); err != nil {
						return fmt.Sprintf("Failed to parse live state: %s", err), nil
					}
					cleanObject(liveObj)
					return marshalCompact(liveObj)
				}
			}

			// Resource not found — list available resources to help the LLM
			var available []string
			for _, r := range resources.Items {
				available = append(available, fmt.Sprintf("%s/%s %s/%s", r.Group, r.Kind, r.Namespace, r.Name))
			}
			log.Debug().Caller().
				Str("app", params.AppName).
				Str("kind", params.Kind).
				Str("name", params.Name).
				Msg("resource not found in managed resources")

			return fmt.Sprintf("Resource %s/%s not found in app %q. Available resources:\n%s",
				params.Kind, params.Name, params.AppName, strings.Join(available, "\n")), nil
		},
	)
}
