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
{{- range .Options }}
|`{{ .Env }}`|{{ .Usage }}|{{ if .Default }}`{{ .Default }}`{{ end }}|
{{- end }}
