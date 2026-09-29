// Package mlxlm is the JSON-over-UDS frontend for TranslateGemma.
// The worker binary is unix/mlx_lm/bin/mlx_lm_server. This package does not spawn it.
package mlxlm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"encore.app/frontend"
	"encore.app/internal/approot"
	"encore.app/internal/modelstate"
)

// BinaryRel is the worker path relative to the app root.
const BinaryRel = "unix/mlx_lm/bin/mlx_lm_server"

type runtime struct{}

// New returns the mlxlm runtime.
func New() frontend.Runtime { return runtime{} }

func (runtime) Kind() modelstate.FrontendKind { return modelstate.FrontendMlxlm }

func (runtime) Start(context.Context) (modelstate.FrontendState, error) {
	root, err := approot.Root()
	if err != nil {
		return modelstate.FrontendStopped, err
	}
	bin := filepath.Join(root, filepath.FromSlash(BinaryRel))
	if _, err := os.Stat(bin); err != nil {
		return modelstate.FrontendStopped, fmt.Errorf("worker binary not present: %s", BinaryRel)
	}
	return modelstate.FrontendStopped, errors.New("worker binary present but this tree does not spawn mlxlm")
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
