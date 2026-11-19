package telemetry

import (
	"context"
	"log/slog"
)

// NewSlogLogger exposes the bridged logger newProviders builds, without installing any globals.
func NewSlogLogger(
	ctx context.Context,
	exporter Exporter,
	opts ...Option,
) (*slog.Logger, func(context.Context) error, error) {
	p, err := newProviders(ctx, exporter, opts...)
	return p.slog, p.shutdown, err
}
