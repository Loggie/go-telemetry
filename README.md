# go-telemetry

Configures global OpenTelemetry trace, metric and log providers, plus runtime metrics.

```go
shutdown, err := telemetry.Setup(ctx, telemetry.ExporterOTLP) // or ExporterStdout, ExporterNone
defer shutdown(context.Background())
```

The OTLP endpoint comes from the standard `OTEL_EXPORTER_OTLP_*` variables.

## Resource attributes

Every span, metric and log carries a *resource* describing where it came from. The resource is built by
*detectors*, which read attributes from the runtime environment. Later detectors override earlier ones.

`Setup` runs these built-in detectors from `go.opentelemetry.io/otel/sdk/resource`, in order:

| Option | Attributes |
| --- | --- |
| `WithTelemetrySDK` | `telemetry.sdk.name`, `telemetry.sdk.language`, `telemetry.sdk.version` |
| `WithService` | `service.name` (fallback `unknown_service:<executable>`), `service.instance.id` |
| `WithHost` / `WithHostID` | `host.name`, `host.id` |
| `WithOS` | `os.type`, `os.description` |
| `WithProcess` | `process.pid`, `process.executable.name`, `process.executable.path`, `process.command_args`, `process.owner`, `process.runtime.*` |
| `WithContainer` | `container.id` |
| `WithFromEnv` | `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`; runs last so the environment always wins |

> `process.command_args` records the full command line. Don't pass secrets as flags.

If a detector can only find some of its attributes (for example, no `/etc/machine-id`, or not running
in a container), `Setup` keeps what was found rather than failing.

## Custom detectors

Platform-specific detectors live in separate modules under `go.opentelemetry.io/contrib/detectors`:

| Platform | Package | Constructor |
| --- | --- | --- |
| AWS EC2 | `go.opentelemetry.io/contrib/detectors/aws/ec2/v2` | `ec2.NewResourceDetector()` |
| AWS ECS | `go.opentelemetry.io/contrib/detectors/aws/ecs` | `ecs.NewResourceDetector()` |
| AWS EKS | `go.opentelemetry.io/contrib/detectors/aws/eks` | `eks.NewResourceDetector()` |
| AWS Lambda | `go.opentelemetry.io/contrib/detectors/aws/lambda` | `lambda.NewResourceDetector()` |
| GCP (GCE, GKE, Cloud Run, App Engine) | `go.opentelemetry.io/contrib/detectors/gcp` | `gcp.NewDetector()` |
| GCP Cloud Functions | `go.opentelemetry.io/contrib/detectors/gcp` | `gcp.NewCloudFunction()` |
| Azure VM | `go.opentelemetry.io/contrib/detectors/azure/azurevm` | `azurevm.New()` |

These query the platform's metadata service for attributes such as `cloud.provider`, `cloud.region`,
`cloud.account.id` and `k8s.cluster.name`.

Pass extra detectors to `Setup` with `WithDetectors`. They run after the built-in detectors (so they can
override generic `host.*` values) but before `WithFromEnv` (so the environment still wins):

```go
shutdown, err := telemetry.Setup(ctx, telemetry.ExporterOTLP,
    telemetry.WithDetectors(gcp.NewDetector()),
)
```
