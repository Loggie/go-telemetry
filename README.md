# go-telemetry

Configures global OpenTelemetry trace, metric and log providers, plus runtime metrics.

```go
shutdown, err := telemetry.Setup(ctx, telemetry.ExporterOTLP) // or ExporterStdout, ExporterNone
defer shutdown(context.Background())
```

The service name and resource attributes come from `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`. The OTLP endpoint comes from the standard `OTEL_EXPORTER_OTLP_*` variables.
