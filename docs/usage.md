# Usage

## Installation

`kubechecks` currently only officially supports deployment to a Kubernetes Cluster via Helm.

### Requirements

1. Kubernetes Cluster
2. Github/Gitlab token (for authenticating to the repository)
3. ArgoCD

### Helm Installation

To get started, add the `kubechecks` repository to Helm:

```console
helm repo add kubechecks <URL HERE>
```

Once installed, simply run:

```console
helm install kubechecks -n kubechecks
```

Refer to [configuration](#configuration) for details about the various options available for customising `kubechecks`.

## Configuration

`kubechecks` can be configured to meet your specific set up
through the use of enviornment variables defined in your provided `values.yaml`. The full list of supported environment variables is described below:

|Env Var|Description|Default Value|
|-------|-----------|-------------|
|`KUBECHECKS_ARGOCD_API_INSECURE`|Configure whether `kubechecks` is allowed to communicate with ArgoCD insecurely|`false`|
|`KUBECHECKS_ARGOCD_API_PATH_PREFIX`|Prefix to apply to ArgoCD API calls made by `kubechecks`|`"/"`|
|`KUBECHECKS_ARGOCD_WEBHOOK_URL`|ArgoCD Webhook URL `kubechecks` should use|`null`|
|`KUBECHECKS_FALLBACK_K8S_VERSION`|Fallback Kubernetes API for running checks against|`"1.22.0"`|
|`KUBECHECKS_LOG_LEVEL`|Log level verbosity, one of `[info, debug, warn, err, fatal]`|`"debug"`|
|`KUBECHECKS_NAMESPACE`|Kubernetes namespace `kubechecks` is deployed in|`kubechecks`|
|`KUBECHECKS_WEBHOOK_URL_BASE`|Base string for `kubechecks` webhook address|`null`|
|`KUBECHECKS_WEBHOOK_URL_PREFIX`|Prefix for `kubechecks` webhook address|`null`|
|`KUBECHECKS_ARGOCD_API_SERVER_ADDR`|???|`null`|
|`KUBECHECKS_OTEL_ENABLED`|Enable OpenTelemetry tracing|`"true"`|
|`KUBECHECKS_OTEL_COLLECTOR_PORT`|OpenTelemetry collector port \(if OTel is enabled\) |`"4317"`|

**Note that the prefix `KUBECHECKS_` is required for all environment variables due to the way the application is designed.**
