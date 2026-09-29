// Package mlxcel is the shared-socket MLX frontend. It does not spawn a process.
package mlxcel

import (
	"context"
	"errors"

	"encore.app/frontend"
	"encore.app/internal/modelstate"
)

type runtime struct{}

// New returns the mlxcel runtime.
func New() frontend.Runtime { return runtime{} }

func (runtime) Kind() modelstate.FrontendKind { return modelstate.FrontendMlxcel }

func (runtime) Start(context.Context) (modelstate.FrontendState, error) {
	return modelstate.FrontendStopped, errors.New("worker binary not present")
}

func (runtime) Stop(context.Context) error { return nil }

func (runtime) Load(context.Context, string, string) error {
	return errors.New("model load is not available in this tree")
}

func (runtime) Unload(context.Context, string) error { return nil }

func (runtime) Ready(context.Context) error { return modelstate.ErrFrontendNotReady }

func (runtime) Get(context.Context, string) (frontend.Live, error) {
	return frontend.Live{}, errors.New("model not loaded")
}
