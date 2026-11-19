package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/contrib/bridges/otelslog"

	"github.com/loggie/go-telemetry"
)

// With an exporter, Setup bridges slog to the OTel logger provider.
func TestSetupBridgesSlog(t *testing.T) {
	t.Parallel()

	logger, shutdown, err := telemetry.NewSlogLogger(t.Context(), telemetry.ExporterStdout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	if logger == nil {
		t.Fatal("logger = nil, want a bridged logger")
	}
	if _, ok := logger.Handler().(*otelslog.Handler); !ok {
		t.Fatalf("handler = %T, want *otelslog.Handler", logger.Handler())
	}
}

// Without an exporter Setup leaves the default stderr logger alone.
func TestSetupNoneKeepsDefaultLogger(t *testing.T) {
	t.Parallel()

	logger, shutdown, err := telemetry.NewSlogLogger(t.Context(), telemetry.ExporterNone)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	if logger != nil {
		t.Fatal("ExporterNone built a logger to replace the default")
	}
}
