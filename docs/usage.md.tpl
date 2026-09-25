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

## Validating against CRDs in the pull request

By default, `kubechecks` validates manifests against the schemas published for your
Kubernetes version plus any schema locations you configure globally. A CustomResourceDefinition
that only exists on the branch under review is not in either place, so a resource that
instantiates it cannot be checked.

Point `KUBECHECKS_REPO_CRD_SCHEMA_PATHS` at the directories that hold your CRDs and
`kubechecks` searches them in the commit being checked, takes each version's
`openAPIV3Schema`, and hands those to kubeconform ahead of every other schema location. A
pull request can then add a CRD and a resource that uses it in the same branch, and the
resource is validated against the definition it ships with.

Schemas found in the commit take precedence over the ones published elsewhere, so a CRD
that the branch changes is checked in its new shape. Kinds with no CRD in the repository
fall through to the usual schema locations, unchanged.

The schema is used exactly as the CRD declares it, which means validation matches what the
API server enforces: types, `required`, `enum`, and bounds are all checked. A field the CRD
does not declare is accepted, just as the API server accepts it and prunes it — misspelled
field names are not reported. If a CRD's schema is one kubeconform cannot compile, that
kind falls back to the other schema locations and the reason is logged.

The value is a comma-separated list of directories relative to the repository root. Leave
it unset to turn the behaviour off, or set it to `.` to search the whole repository —
worth narrowing on a large monorepo, since every run walks these paths.

```yaml
env:
  - name: KUBECHECKS_REPO_CRD_SCHEMA_PATHS
    value: "crds,charts/platform/crds"
```

Resources validated against a CRD from the commit are listed in the kubeconform section of
the report, along with the file each definition came from.

This setting has no effect when `KUBECHECKS_ENABLE_KUBECONFORM` is disabled.

## Configuration

`kubechecks` can be configured to meet your specific set up through the use of enviornment variables defined in your provided `values.yaml`.

The full list of supported environment variables is described below:

|Env Var|Description|Default Value|
|-----------|-------------|------|
{{- range .Options }}
|`{{ .Env }}`|{{ .Usage }}|{{ if .Default }}`{{ .Default }}`{{ end }}|
{{- end }}
