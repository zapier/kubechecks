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
be a full kubeconform path template, used exactly as written.

The [CRDs-catalog](https://github.com/datreeio/CRDs-catalog) layout — one directory per API
group, holding `<kind>_<version>.json` — is what you get from the catalog itself and from
`openapi2jsonschema` run per group. It needs no renaming, just `{{ .Group }}`:

```yaml
env:
  - name: KUBECHECKS_SCHEMAS_LOCATION
    # .github/schemas/external-secrets.io/externalsecret_v1.json
    value: ".github/schemas/{{ .Group }}/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json"
```

Drop the `{{ .Group }}/` segment if your files are flat in one directory instead.

The variables are:

|Variable|For `external-secrets.io/v1 ExternalSecret`|
|--------|--------------------------------------------|
|`.ResourceKind`|`externalsecret` — always lowercased|
|`.ResourceAPIVersion`|`v1` — the version alone|
|`.Group`|`external-secrets.io` — the full API group|
|`.KindSuffix`|`-external-secrets-v1` — only the group's first label|
|`.NormalizedKubernetesVersion`|`1.30.0`|

Note that a templated location is used verbatim, so it only looks under a Kubernetes version
directory if you ask it to. If a kind is reported as having no schema, the report lists every
location that was searched, which is usually enough to spot a template that does not match how
the files are laid out.

A relative location that is not a directory in the commit being checked is logged and skipped,
rather than silently contributing nothing. These are ordinary JSON Schema files wherever they
come from; only how the location is resolved differs.

## Configuration

`kubechecks` can be configured to meet your specific set up through the use of enviornment variables defined in your provided `values.yaml`.

The full list of supported environment variables is described below:

|Env Var|Description|Default Value|
|-----------|-------------|------|
|`KUBECHECKS_ADDITIONAL_APPS_NAMESPACES`|Additional namespaces other than the ArgoCDNamespace to monitor for applications.|`[]`|
|`KUBECHECKS_AI_REVIEW_EXTRA_INSTRUCTIONS`|Extra instructions appended to the AI review prompt. Use for org-wide policies (e.g. 'all deployments must have resource limits').||
|`KUBECHECKS_AI_REVIEW_MAX_APPS`|Maximum number of apps to AI review per MR/PR. Apps beyond this cap are skipped.|`10`|
|`KUBECHECKS_AI_REVIEW_MAX_TURNS`|Maximum tool use iterations for AI review.|`20`|
|`KUBECHECKS_AI_REVIEW_MODEL`|AI review model ID.|`claude-sonnet-4-6`|
|`KUBECHECKS_AI_REVIEW_POST_SUGGESTIONS`|Post AI-generated inline code suggestions as PR/MR review comments. When false, the AI review summary comment is still posted but inline suggestions are suppressed.|`false`|
|`KUBECHECKS_AI_REVIEW_PROVIDER`|AI review provider. One of anthropic, openai.|`anthropic`|
|`KUBECHECKS_AI_REVIEW_SYSTEM_PROMPT`|Custom system prompt for AI review. Overrides the default review instructions.||
|`KUBECHECKS_AI_REVIEW_TIMEOUT`|Timeout per AI review.|`5m0s`|
|`KUBECHECKS_ANTHROPIC_API_KEY`|Anthropic API key for AI review.||
|`KUBECHECKS_ARCHIVE_CACHE_DIR`|Directory for archive cache.|`/tmp/kubechecks/archives`|
|`KUBECHECKS_ARCHIVE_CACHE_TTL`|Time-to-live for cached archives.|`1h0m0s`|
|`KUBECHECKS_ARGOCD_API_INSECURE`|Enable to use insecure connections over TLS to the ArgoCD API server.|`false`|
|`KUBECHECKS_ARGOCD_API_NAMESPACE`|ArgoCD namespace where the application watcher will read Custom Resource Definitions (CRD) for Application and ApplicationSet resources.|`argocd`|
|`KUBECHECKS_ARGOCD_API_PLAINTEXT`|Enable to use plaintext connections without TLS.|`false`|
|`KUBECHECKS_ARGOCD_API_SERVER_ADDR`|ArgoCD API Server Address.|`argocd-server`|
|`KUBECHECKS_ARGOCD_API_TOKEN`|ArgoCD API token.||
|`KUBECHECKS_ARGOCD_REPOSITORY_ENDPOINT`|Location of the argocd repository service endpoint.|`argocd-repo-server.argocd:8081`|
|`KUBECHECKS_ARGOCD_REPOSITORY_INSECURE`|True if you need to skip validating the grpc tls certificate.|`true`|
|`KUBECHECKS_ARGOCD_SEND_FULL_REPOSITORY`|Set to true if you want to try to send the full repository to ArgoCD when generating manifests.|`false`|
|`KUBECHECKS_CHART_CACHE_DIR`|Directory for caching downloaded Helm charts for AI review.|`/tmp/kubechecks/charts`|
|`KUBECHECKS_ENABLE_AI_DIFF_SUMMARY`|Enable AI-powered diff summary. Requires openai-api-token or anthropic-api-key.|`false`|
|`KUBECHECKS_ENABLE_AI_REVIEW`|Enable AI-powered impact review of manifest changes.|`false`|
|`KUBECHECKS_ENABLE_CONFTEST`|Set to true to enable conftest policy checking of manifests.|`false`|
|`KUBECHECKS_ENABLE_HOOKS_RENDERER`|Render hooks.|`true`|
|`KUBECHECKS_ENABLE_KUBECONFORM`|Enable kubeconform checks.|`true`|
|`KUBECHECKS_ENABLE_PREUPGRADE`|Enable preupgrade checks.|`true`|
|`KUBECHECKS_ENSURE_WEBHOOKS`|Ensure that webhooks are created in repositories referenced by argo.|`false`|
|`KUBECHECKS_FALLBACK_K8S_VERSION`|Fallback target Kubernetes version for schema / upgrade checks.|`1.23.0`|
|`KUBECHECKS_GITHUB_APP_ID`|Github App ID.|`0`|
|`KUBECHECKS_GITHUB_INSTALLATION_ID`|Github Installation ID.|`0`|
|`KUBECHECKS_GITHUB_PRIVATE_KEY`|Github App Private Key.||
|`KUBECHECKS_IDENTIFIER`|Identifier for the kubechecks instance. Used to differentiate between multiple kubechecks instances.||
|`KUBECHECKS_KUBEPUG_GENERATED_STORE`|URL for the kubepug generated store.|`https://kubepug.xyz/data/data.json`|
|`KUBECHECKS_KUBERNETES_CLUSTERID`|Kubernetes Cluster ID, must be specified if kubernetes-type is eks.||
|`KUBECHECKS_KUBERNETES_CONFIG`|Path to your kubernetes config file, used to monitor applications.||
|`KUBECHECKS_KUBERNETES_TYPE`|Kubernetes Type One of eks, or local.|`local`|
|`KUBECHECKS_LABEL_FILTER`|(Optional) If set, The label that must be set on an MR (as "kubechecks:<value>") for kubechecks to process the merge request webhook.||
|`KUBECHECKS_LOG_LEVEL`|Set the log output level. One of error, warn, info, debug, trace.|`info`|
|`KUBECHECKS_MAX_COMMENTS_PER_CHECK`|Maximum number of comments posted per check run, 1 to 999. What does not fit is left out of the report, with a warning.|`999`|
|`KUBECHECKS_MAX_CONCURRENT_CHECKS`|Number of concurrent checks to run.|`32`|
|`KUBECHECKS_MAX_QUEUE_SIZE`|Size of app diff check queue.|`1024`|
|`KUBECHECKS_MAX_REPO_WORKER_QUEUE_SIZE`|Maximum size of check request queue per repository worker.|`100`|
|`KUBECHECKS_MONITOR_ALL_APPLICATIONS`|Monitor all applications in argocd automatically.|`true`|
|`KUBECHECKS_OPENAI_API_TOKEN`|OpenAI API Token.||
|`KUBECHECKS_OTEL_COLLECTOR_HOST`|The OpenTelemetry collector host.||
|`KUBECHECKS_OTEL_COLLECTOR_PORT`|The OpenTelemetry collector port.||
|`KUBECHECKS_OTEL_ENABLED`|Enable OpenTelemetry.|`false`|
|`KUBECHECKS_PERSIST_LOG_LEVEL`|Persists the set log level down to other module loggers.|`false`|
|`KUBECHECKS_POLICIES_LOCATION`|Sets rego policy locations to be used for every check request. Can be common path inside the repos being checked or git urls in either git or http(s) format.|`[./policies]`|
|`KUBECHECKS_REPLAN_COMMENT_MSG`|comment message which re-triggers kubechecks on PR.|`kubechecks again`|
|`KUBECHECKS_REPO_CACHE_DIR`|Directory for persistent repository cache.|`/tmp/kubechecks/repos`|
|`KUBECHECKS_REPO_CACHE_ENABLED`|Enable persistent repository caching.|`true`|
|`KUBECHECKS_REPO_CACHE_TTL`|Time-to-live for cached repositories.|`24h0m0s`|
|`KUBECHECKS_REPO_REFRESH_INTERVAL`|Interval between static repo refreshes (for schemas and policies).|`5m`|
|`KUBECHECKS_SCHEMAS_LOCATION`|Sets schema locations to be used for every check request. An absolute path on the host, or a git url in either git or http(s) format, is used as given. A relative path names a directory inside the repository being checked, so that schemas committed alongside the manifests that use them are found. Any location may be a kubeconform path template, such as ".github/schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json", when the files are not named "<kind>-<group>-<version>.json".|`[]`|
|`KUBECHECKS_SHOW_DEBUG_INFO`|Set to true to print debug info to the footer of MR comments.|`false`|
|`KUBECHECKS_TIDY_OUTDATED_COMMENTS_MODE`|Sets the mode to use when tidying outdated comments. One of hide, delete.|`hide`|
|`KUBECHECKS_VCS_BASE_URL`|VCS base url, useful if self hosting gitlab, enterprise github, etc.||
|`KUBECHECKS_VCS_EMAIL`|VCS Email.||
|`KUBECHECKS_VCS_TOKEN`|VCS API token.||
|`KUBECHECKS_VCS_TYPE`|VCS type. One of gitlab or github.|`gitlab`|
|`KUBECHECKS_VCS_UPLOAD_URL`|VCS upload url, required for enterprise github.||
|`KUBECHECKS_VCS_USERNAME`|VCS Username.||
|`KUBECHECKS_WEBHOOK_SECRET`|Optional secret key for validating the source of incoming webhooks.||
|`KUBECHECKS_WEBHOOK_URL_BASE`|The endpoint to listen on for incoming PR/MR event webhooks. For example, 'https://checker.mycompany.com'.||
|`KUBECHECKS_WEBHOOK_URL_PREFIX`|If your application is running behind a proxy that uses path based routing, set this value to match the path prefix. For example, '/hello/world'.||
|`KUBECHECKS_WORST_AI_REVIEW_STATE`|The worst state that can be returned from AI review.|`warning`|
|`KUBECHECKS_WORST_CONFTEST_STATE`|The worst state that can be returned from conftest.|`panic`|
|`KUBECHECKS_WORST_HOOKS_STATE`|The worst state that can be returned from the hooks renderer.|`panic`|
|`KUBECHECKS_WORST_KUBECONFORM_STATE`|The worst state that can be returned from kubeconform.|`panic`|
|`KUBECHECKS_WORST_PREUPGRADE_STATE`|The worst state that can be returned from preupgrade checks.|`panic`|
