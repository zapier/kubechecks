# Usage

## Installation

`kubechecks` currently only officially supports deployment to a Kubernetes Cluster via Helm.

### Requirements

1. Kubernetes Cluster
2. Github/Gitlab token (for authenticating to the repository)
3. ArgoCD

### Helm Installation

To get started, add the `kubechecks` repository to Helm:

# TODO TODO TODO TODO Add a URL
```console
helm repo add kubechecks <URL HERE> 
```

Once installed, simply run:

```console
helm install kubechecks -n kubechecks --create-namespace
```

Refer to [configuration](#configuration) for details about the various options available for customising `kubechecks`.

## Configuration

`kubechecks` can be configured to meet your specific set up
through the use of enviornment variables defined in your provided `values.yaml`. The full list of supported environment variables is described below:

|Env Var|Description|Default Value|
|-------|-----------|-------------|
|`KUBECHECKS_ARGOCD_API_INSECURE`|Configure whether `kubechecks` is allowed to communicate with ArgoCD insecurely|`false`|
|`KUBECHECKS_ARGOCD_API_PATH_PREFIX`|Prefix to apply to ArgoCD API calls made by `kubechecks`|`"/"`|
|`KUBECHECKS_ARGOCD_API_SERVER_ADDR`|ArgoCD API Server Address|`null`|
|`KUBECHECKS_ARGOCD_WEBHOOK_URL`|ArgoCD Webhook URL `kubechecks` should use|`null`|
|`KUBECHECKS_FALLBACK_K8S_VERSION`|Fallback target Kubernetes version for schema / upgrade checks|`"1.22.0"`|
|`KUBECHECKS_LOG_LEVEL`|Log level verbosity, one of `[info, debug, trace]`|`"info"`|
|`KUBECHECKS_NAMESPACE`|Kubernetes namespace `kubechecks` is deployed in|`kubechecks`|
|`KUBECHECKS_WEBHOOK_URL_BASE`|The URL where KubeChecks receives webhooks from|`null`|
|`KUBECHECKS_WEBHOOK_URL_PREFIX`|If your application is running behind a proxy that uses path based routing, set this value to match the path prefix.|`null`|
|`KUBECHECKS_WEBHOOK_SECRET`|Optional secret key for validating the source of incoming webhooks.|`""`|
|`KUBECHECKS_OTEL_ENABLED`|Enable OpenTelemetry tracing|`false`|
|`KUBECHECKS_OTEL_COLLECTOR_PORT`|OpenTelemetry collector port \(if OTel is enabled\) |`null`|
|`KUBECHECKS_OTEL_COLLECTOR_HOST`|The OpenTelemetry collector host|`null`| 
|`KUBECHECKS_SHOW_DEBUG_INFO`| Set to true to print debug info to the footer of MR comments | `false`|
|`KUBECHECKS_VCS_TYPE`| Which VCS Client to utilise (one of `gitlab` or `github`) | `gitlab`|
|`KUBECHECKS_LABEL_FILTER`|If set, the label that must be set on an PR/MR (as "kubechecks:<value>") for kubechecks to process the merge request webhook|`null`|

The following configuraion is done via Kubernetes Secrets; ensure these are specified under the `secrets` section of `values.yaml` or through your chosen secrets provider before attempting to run `kubechecks`:

|Secret|Description|Default Value|
|-------|-----------|-------------|
|`KUBECHECKS_VCS_TOKEN`| VCS API Token for communicating with your VCS provider | `null`|
|`KUBECHECKS_ARGOCD_API_TOKEN`| ArgoCD API Token for communicating with your ArgoCD installation| `null`|
|`KUBECHECKS_OPENAI_API_TOKEN`| OpenAI API Token for generating AI diff summaries |`null`|


**Note that the prefix `KUBECHECKS_` is required for all environment variables due to the way the application is designed.**
