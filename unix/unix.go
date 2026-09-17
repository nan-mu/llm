// Package unix is the frontend runtime library for llama-server and mlxcel-server.
// It is not an Encore service.
package unix

import (
	"context"
	"net/http"

	"encore.app/internal/modelstate"
)

// Model is one frontend-native model as reported by llama-server / mlxcel-server.
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
	EnsureReady(ctx context.Context) error
	EnsureLoaded(ctx context.Context, id string) error
}

// Inferencer is the gateway-plane surface. This slice does not implement it.
type Inferencer interface {
	ChatCompletions(ctx context.Context, w http.ResponseWriter, req *http.Request) error
	Transcribe(ctx context.Context, w http.ResponseWriter, req *http.Request) error
}
