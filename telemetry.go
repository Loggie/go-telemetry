// Package telemetry configures the global OpenTelemetry trace, metric and log providers.
package telemetry

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
)

// Exporter selects where telemetry is sent.
type Exporter string

const (
	// ExporterOTLP sends to an OTLP/gRPC endpoint, configured with the standard OTEL_EXPORTER_OTLP_* variables.
	ExporterOTLP Exporter = "otlp"
	// ExporterStdout pretty-prints to stdout, for local debugging.
	ExporterStdout Exporter = "stdout"
	// ExporterNone installs providers without exporters, so instrumentation is a cheap no-op.
	ExporterNone Exporter = "none"
)

type Option func(*config)

type config struct {
	detectors []resource.Detector
}

// WithDetectors adds resource detectors that run after the build-in ones.
func WithDetectors(d ...resource.Detector) Option {
	return func(c *config) {
		c.detectors = append(c.detectors, d...)
	}
}

// Setup installs global providers and starts runtime metrics. The service name and other resource
// attributes come from OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES. Call the returned function on shutdown.
func Setup(ctx context.Context, exporter Exporter, opts ...Option) (shutdown func(context.Context) error, err error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}

	var shutdownFuncs []func(context.Context) error
	shutdown = func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		shutdownFuncs = nil
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, shutdown(ctx))
		}
	}()

	// Later options override earlier ones, so environment-provided attributes come last.
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithService(),
		resource.WithHost(),
		resource.WithHostID(),
		resource.WithOS(),
		resource.WithProcess(),
		resource.WithContainer(),
		resource.WithDetectors(cfg.detectors...),
		resource.WithFromEnv(),
	)

	// Some detectors (host ID, process owner, container ID) can't always find their source, e.g. a
	// distroless image has no /etc/machine-id. Keep whatever was detected rather than failing setup.
	if err != nil {
		if res == nil {
			return shutdown, fmt.Errorf("telemetry resource: %w", err)
		}
		otel.Handle(fmt.Errorf("telemetry resource (partial): %w", err))
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	tracerProvider, err := newTracerProvider(ctx, exporter, res)
	if err != nil {
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	meterProvider, err := newMeterProvider(ctx, exporter, res)
	if err != nil {
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	loggerProvider, err := newLoggerProvider(ctx, exporter, res)
	if err != nil {
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	otel.SetLoggerProvider(loggerProvider)

	if err = runtime.Start(runtime.WithMeterProvider(meterProvider)); err != nil {
		return shutdown, err
	}

	return shutdown, nil
}

func newTracerProvider(ctx context.Context, exporter Exporter, res *resource.Resource) (*trace.TracerProvider, error) {
	opts := []trace.TracerProviderOption{trace.WithResource(res)}

	switch exporter {
	case ExporterOTLP:
		exp, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, err
		}
		opts = append(opts, trace.WithBatcher(exp))
	case ExporterStdout:
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, err
		}
		opts = append(opts, trace.WithBatcher(exp))
	case ExporterNone:
	default:
		return nil, fmt.Errorf("unknown telemetry exporter %q", exporter)
	}

	return trace.NewTracerProvider(opts...), nil
}

func newMeterProvider(ctx context.Context, exporter Exporter, res *resource.Resource) (*metric.MeterProvider, error) {
	opts := []metric.Option{metric.WithResource(res)}

	switch exporter {
	case ExporterOTLP:
		exp, err := otlpmetricgrpc.New(ctx)
		if err != nil {
			return nil, err
		}
		opts = append(opts, metric.WithReader(metric.NewPeriodicReader(exp)))
	case ExporterStdout:
		exp, err := stdoutmetric.New(stdoutmetric.WithPrettyPrint())
		if err != nil {
			return nil, err
		}
		opts = append(opts, metric.WithReader(metric.NewPeriodicReader(exp)))
	case ExporterNone:
	default:
		return nil, fmt.Errorf("unknown telemetry exporter %q", exporter)
	}

	return metric.NewMeterProvider(opts...), nil
}

func newLoggerProvider(ctx context.Context, exporter Exporter, res *resource.Resource) (*log.LoggerProvider, error) {
	opts := []log.LoggerProviderOption{log.WithResource(res)}

	switch exporter {
	case ExporterOTLP:
		exp, err := otlploggrpc.New(ctx)
		if err != nil {
			return nil, err
		}
		opts = append(opts, log.WithProcessor(log.NewBatchProcessor(exp)))
	case ExporterStdout:
		exp, err := stdoutlog.New(stdoutlog.WithPrettyPrint())
		if err != nil {
			return nil, err
		}
		opts = append(opts, log.WithProcessor(log.NewBatchProcessor(exp)))
	case ExporterNone:
	default:
		return nil, fmt.Errorf("unknown telemetry exporter %q", exporter)
	}

	return log.NewLoggerProvider(opts...), nil
}
