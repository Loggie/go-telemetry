package telemetry_test

import (
	"context"
	"testing"

	telemetry "github.com/loggie/go-telemetry"
)

//nolint:paralleltest // Setup installs global providers
func TestSetupWithMetrics(t *testing.T) {
	shutdown, err := telemetry.Setup(t.Context(), telemetry.WithRuntimeMetrics(), telemetry.WithHostMetrics())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err = shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
