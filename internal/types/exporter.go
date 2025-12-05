package types

import (
	"context"
)

// Exporter is the interface for backend exporters
type Exporter interface {
	Name() string
	Export(ctx context.Context, metrics []any) error
	Shutdown(ctx context.Context) error
}
