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

## Schema locations

`kubechecks` validates manifests against the schemas published for your Kubernetes version,
plus any locations set in `KUBECHECKS_SCHEMAS_LOCATION`. A custom resource with no schema in
either place is reported as `could not find schema for <kind>`.

`KUBECHECKS_SCHEMAS_LOCATION` is a comma-separated list, searched in the order given. How each
entry is interpreted depends on its form:

|Form|Example|Meaning|
|----|-------|-------|
|Absolute path|`/schemas`|A directory on the host running `kubechecks`|
|Git url|`git@github.com:org/schemas.git`|Cloned at startup and refreshed periodically|
|http(s) url|`https://example.com/schemas`|Fetched per resource|
|**Relative path**|`.github/schemas`|**A directory inside the repository being checked**|

A relative location is resolved against the checkout of the pull request under review, so
schemas committed alongside the manifests that use them are found — and a schema the branch
adds or changes is the one validated against. Kinds with no schema there fall through to the
remaining locations unchanged.

```yaml
env:
  - name: KUBECHECKS_SCHEMAS_LOCATION
    value: ".github/schemas"
```

### Naming

By default a location is a directory, and `kubechecks` looks in it for
`<k8s version>/<kind>-<group>-<version>.json` — all lowercase, where `<group>` is only the
first label of the API group. This is the layout of the published Kubernetes schemas.

Schemas kept in a repository are usually organised differently, so any location may instead
be a full kubeconform path template, used exactly as written. `openapi2jsonschema` and the
[CRDs-catalog](https://github.com/datreeio/CRDs-catalog) both write flat
`<kind>_<version>.json` files, which need no renaming:

```yaml
env:
  - name: KUBECHECKS_SCHEMAS_LOCATION
    value: ".github/schemas/{{ "{{" }} .ResourceKind {{ "}}" }}_{{ "{{" }} .ResourceAPIVersion {{ "}}" }}.json"
```

The variables are `.ResourceKind` (lowercased), `.ResourceAPIVersion` (the version alone),
`.Group` (the full API group), `.KindSuffix` (`-<group label>-<version>`) and
`.NormalizedKubernetesVersion`. Note that a templated location is used verbatim, so it only
looks under a Kubernetes version directory if you ask it to.

A relative location that is not a directory in the commit being checked is logged and skipped,
rather than silently contributing nothing. These are ordinary JSON Schema files wherever they
come from; only how the location is resolved differs.

## Configuration

`kubechecks` can be configured to meet your specific set up through the use of enviornment variables defined in your provided `values.yaml`.

The full list of supported environment variables is described below:

|Env Var|Description|Default Value|
|-----------|-------------|------|
{{- range .Options }}
|`{{ .Env }}`|{{ .Usage }}|{{ if .Default }}`{{ .Default }}`{{ end }}|
{{- end }}
