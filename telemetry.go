// Package telemetry configures the global OpenTelemetry trace, metric and log providers.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
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
	logScope  string
}

// WithDetectors adds resource detectors that run after the build-in ones.
func WithDetectors(d ...resource.Detector) Option {
	return func(c *config) {
		c.detectors = append(c.detectors, d...)
	}
}

// WithLogScope sets the instrumentation scope name on records from the default slog logger,
// typically the application's module path. If unset, the scope name is empty.
func WithLogScope(name string) Option {
	return func(c *config) {
		c.logScope = name
	}
}

// Setup installs global providers and starts runtime metrics. The service name and other resource
// attributes come from OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES. Call the returned function on shutdown.
//
// Unless exporter is ExporterNone, Setup also makes slog's default logger write to the OTel logger provider
// instead of stderr. Log with a context ([slog.LogAttrs], [slog.InfoContext], ...) so records are linked to the span.
func Setup(ctx context.Context, exporter Exporter, opts ...Option) (func(context.Context) error, error) {
	p, err := newProviders(ctx, exporter, opts...)
	if err != nil {
		return p.shutdown, err
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	otel.SetTracerProvider(p.tracer)
	otel.SetMeterProvider(p.meter)
	otel.SetLoggerProvider(p.logger)
	if p.slog != nil {
		slog.SetDefault(p.slog)
	}

	if err = runtime.Start(runtime.WithMeterProvider(p.meter)); err != nil {
		return p.shutdown, errors.Join(err, p.shutdown(ctx))
	}

	return p.shutdown, nil
}

// providers holds everything Setup installs globally, so it can be built and tested without
// touching process-wide state.
type providers struct {
	tracer *trace.TracerProvider
	meter  *metric.MeterProvider
	logger *log.LoggerProvider
	// slog is the logger bridged to the OTel logger provider; nil for ExporterNone.
	slog          *slog.Logger
	shutdownFuncs []func(context.Context) error
}

// shutdown stops every provider built so far. It is safe to call more than once.
func (p *providers) shutdown(ctx context.Context) error {
	var errs error
	for _, fn := range p.shutdownFuncs {
		errs = errors.Join(errs, fn(ctx))
	}
	p.shutdownFuncs = nil
	return errs
}

// fail shuts down what was built so far and returns err joined with any shutdown error.
func (p *providers) fail(ctx context.Context, err error) (*providers, error) {
	return p, errors.Join(err, p.shutdown(ctx))
}

// newProviders builds the trace, metric and log providers and the bridged slog logger. On error, the
// returned providers are already shut down and still safe to use for shutdown.
func newProviders(ctx context.Context, exporter Exporter, opts ...Option) (*providers, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	p := &providers{}

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
			return p, fmt.Errorf("telemetry resource: %w", err)
		}
		otel.Handle(fmt.Errorf("telemetry resource (partial): %w", err))
	}

	p.tracer, err = newTracerProvider(ctx, exporter, res)
	if err != nil {
		return p.fail(ctx, err)
	}
	p.shutdownFuncs = append(p.shutdownFuncs, p.tracer.Shutdown)

	p.meter, err = newMeterProvider(ctx, exporter, res)
	if err != nil {
		return p.fail(ctx, err)
	}
	p.shutdownFuncs = append(p.shutdownFuncs, p.meter.Shutdown)

	p.logger, err = newLoggerProvider(ctx, exporter, res)
	if err != nil {
		return p.fail(ctx, err)
	}
	p.shutdownFuncs = append(p.shutdownFuncs, p.logger.Shutdown)

	if exporter != ExporterNone {
		p.slog = slog.New(otelslog.NewHandler(cfg.logScope, otelslog.WithLoggerProvider(p.logger)))
	}

	return p, nil
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
