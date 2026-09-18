// Package unix is the frontend runtime library for llama-server, mlxcel-server, and mlxlm.
// It is not an Encore service.
package unix

import (
	"context"
	"net/http"

	"encore.app/internal/modelstate"
)

// Model is one frontend-native model as reported by a Unix frontend.
type Model struct {
	ID    string
	Path  string
	State modelstate.ModelState
}

// Runtime is the control-plane surface: process + residency.
// gateway must not depend on this interface.
type Runtime interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Ready(ctx context.Context) error
	Load(ctx context.Context, id string) error
	Unload(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (Model, error)
	List(ctx context.Context) ([]Model, error)
	EnsureReady(ctx context.Context) error
	EnsureLoaded(ctx context.Context, id string) error
	// ModelPID returns the OS pid for a loaded model, or an error if not found/running.
	ModelPID(ctx context.Context, id string) (int, error)
	// BackendPIDs returns live frontend process ids (empty if none).
	BackendPIDs(ctx context.Context) ([]int, error)
}

// Inferencer is the gateway-plane surface (chat/transcribe proxy).
type Inferencer interface {
	ChatCompletions(ctx context.Context, w http.ResponseWriter, req *http.Request) error
	Transcribe(ctx context.Context, w http.ResponseWriter, req *http.Request) error
}
