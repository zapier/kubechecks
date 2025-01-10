# Integrations

`kubechecks` supports various integrations to enhance its functionality. Below are the integrations and how to configure them:

### OpenAI

OpenAI is used to explain the diffs 

#### Configuration

To enable OpenAI diff explanation, set the following environment variable in your `values.yaml`:

```yaml
KUBECHECKS_OPENAI_API_TOKEN: true
```

### Conftest

Conftest is used for policy checking of manifests.

#### Configuration

To enable Conftest, set the following environment variable in your `values.yaml`:

```yaml
KUBECHECKS_ENABLE_CONFTEST: true
```

### Kubeconform

Kubeconform is used for validating Kubernetes manifests against the Kubernetes OpenAPI schemas.

#### Configuration

To enable Kubeconform, set the following environment variable in your `values.yaml`:

```yaml
KUBECHECKS_ENABLE_KUBECONFORM: true
```

### Kyverno

Kyverno is used for policy checks. This kyverno integration uses the `kyverno-cli` and only supports basic policies - policies without variables and contexts because these policies will require and external values configuration.

#### Configuration

To enable Kyverno, set the following environment variable in your `values.yaml`:

```yaml
KUBECHECKS_ENABLE_KYVERNO_CHECKS: true
```

Additionally, you'll need to set the location of Kyverno policies:

```yaml
KUBECHECKS_KYVERNO_POLICIES_LOCATION: <your-policies-location>
```

### OpenTelemetry

OpenTelemetry is used for observability and tracing.

#### Configuration

To enable OpenTelemetry, set the following environment variables in your `values.yaml`:

```yaml
KUBECHECKS_OTEL_ENABLED: true
KUBECHECKS_OTEL_COLLECTOR_HOST: <your-otel-collector-host>
KUBECHECKS_OTEL_COLLECTOR_PORT: <your-otel-collector-port>
```