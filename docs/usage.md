# Usage

## Installation

`kubechecks` currently only officially supports deployment to a Kubernetes Cluster via Helm.

### Requirements

1. Kubernetes Cluster
2. Github/Gitlab token (for authenticating to the repository)
3. ArgoCD

### Helm Installation

To get started, add the `kubechecks` repository to Helm:

# Add kubechecks helm chart repo

```console
helm repo add kubechecks https://zapier.github.io/kubechecks/
```

Once installed, simply run:

```console
helm install kubechecks charts/kubechecks -n kubechecks --create-namespace
```

Refer to [configuration](#configuration) for details about the various options available for customising `kubechecks`. You **must** provide the required secrets in some capacity; refer to the chart for more details

## Configuration

`kubechecks` can be configured to meet your specific set up through the use of enviornment variables defined in your provided `values.yaml`.

The full list of supported environment variables is described below:

|Env Var|Description|Default Value|
|-----------|-------------|------|
|`KUBECHECKS_ARGOCD_API_INSECURE`|Enable to use insecure connections to the ArgoCD API server.|`false`|
|`KUBECHECKS_ARGOCD_API_SERVER_ADDR`|ArgoCD API Server Address.|`argocd-server`|
|`KUBECHECKS_ARGOCD_API_TOKEN`|ArgoCD API token.||
|`KUBECHECKS_ENABLE_CONFTEST`|Set to true to enable conftest policy checking of manifests.|`false`|
|`KUBECHECKS_ENSURE_WEBHOOKS`|Ensure that webhooks are created in repositories referenced by argo.|`false`|
|`KUBECHECKS_FALLBACK_K8S_VERSION`|Fallback target Kubernetes version for schema / upgrade checks.|`1.23.0`|
|`KUBECHECKS_LABEL_FILTER`|(Optional) If set, The label that must be set on an MR (as "kubechecks:<value>") for kubechecks to process the merge request webhook.||
|`KUBECHECKS_LOG_LEVEL`|Set the log output level. One of error, warn, info, debug, trace.|`info`|
|`KUBECHECKS_MONITOR_ALL_APPLICATIONS`|Monitor all applications in argocd automatically.|`false`|
|`KUBECHECKS_OPENAI_API_TOKEN`|OpenAI API Token.||
|`KUBECHECKS_OTEL_COLLECTOR_HOST`|The OpenTelemetry collector host.||
|`KUBECHECKS_OTEL_COLLECTOR_PORT`|The OpenTelemetry collector port.||
|`KUBECHECKS_OTEL_ENABLED`|Enable OpenTelemetry.|`false`|
|`KUBECHECKS_PERSIST_LOG_LEVEL`|Persists the set log level down to other module loggers.|`false`|
|`KUBECHECKS_POLICIES_LOCATION`|Sets rego policy locations to be used for every check request. Can be common path inside the repos being checked or git urls in either git or http(s) format.|`[./policies]`|
|`KUBECHECKS_SCHEMAS_LOCATION`|Sets schema locations to be used for every check request. Can be common paths inside the repos being checked or git urls in either git or http(s) format.|`[./schemas]`|
|`KUBECHECKS_SHOW_DEBUG_INFO`|Set to true to print debug info to the footer of MR comments.|`false`|
|`KUBECHECKS_TIDY_OUTDATED_COMMENTS_MODE`|Sets the mode to use when tidying outdated comments. One of hide, delete.|`hide`|
|`KUBECHECKS_VCS_BASE_URL`|VCS base url, useful if self hosting gitlab, enterprise github, etc.||
|`KUBECHECKS_VCS_TOKEN`|VCS API token.||
|`KUBECHECKS_VCS_TYPE`|VCS type. One of gitlab or github.|`gitlab`|
|`KUBECHECKS_WEBHOOK_SECRET`|Optional secret key for validating the source of incoming webhooks.||
|`KUBECHECKS_WEBHOOK_URL_BASE`|The endpoint to listen on for incoming PR/MR event webhooks. For example, 'https://checker.mycompany.com'.||
|`KUBECHECKS_WEBHOOK_URL_PREFIX`|If your application is running behind a proxy that uses path based routing, set this value to match the path prefix. For example, '/hello/world'.||
