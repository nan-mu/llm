// Package frontend is a library, not an Encore service.
// Only control may Start, Stop, Load, or Unload a runtime.
package frontend

import (
	"context"

	"encore.app/internal/modelstate"
)

// Live is a point-in-time view of one model on a running frontend.
type Live struct {
	Observed modelstate.ModelState
	PID      *int64
	MemoryMB *int64
}

// Runtime is one Unix frontend (llama, mlxcel, or mlxlm).
type Runtime interface {
	Kind() modelstate.FrontendKind
	// Start moves the frontend toward READY. A missing worker binary returns
	// an error and does not spawn a process.
	Start(ctx context.Context) (modelstate.FrontendState, error)
	Stop(ctx context.Context) error
	Load(ctx context.Context, nativeID, weightsPath string) error
	Unload(ctx context.Context, nativeID string) error
	Ready(ctx context.Context) error
	Get(ctx context.Context, nativeID string) (Live, error)
}
