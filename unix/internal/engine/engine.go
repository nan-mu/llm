package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"encore.app/internal/modelstate"
	"encore.app/unix"
	"encore.app/unix/internal/httpx"
	"encore.app/unix/internal/logx"
	"encore.app/unix/internal/proc"
)

const (
	defaultReadyTimeout = 30 * time.Second
	healthPoll          = 100 * time.Millisecond
)

// Engine is the shared Start/Stop/Load implementation used by Unix frontends.
type Engine struct {
	kind   string
	proc   *proc.Proc
	client *httpx.Client
	mu     sync.Mutex
}

func New(kind string, p *proc.Proc, client *httpx.Client) *Engine {
	return &Engine{kind: kind, proc: p, client: client}
}

func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.readyLocked(ctx); err == nil {
		return nil
	}

	waitCtx, cancel := withReadyTimeout(ctx)
	defer cancel()

	if err := e.proc.Start(waitCtx); err != nil {
		return unix.WrapUnavailable("start frontend", err)
	}
	if err := e.waitHealthy(waitCtx); err != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = e.proc.Stop(stopCtx)
		return err
	}
	logx.Info("frontend ready",
		"event", "frontend.ready",
		"kind", e.kind,
	)
	return nil
}

func (e *Engine) Stop(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.proc.Stop(ctx); err != nil {
		return unix.WrapUnavailable("stop frontend", err)
	}
	return nil
}

func (e *Engine) Ready(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.readyLocked(ctx)
}

func (e *Engine) Load(ctx context.Context, id string) error {
	if err := e.Ready(ctx); err != nil {
		return err
	}
	// llama-server / mlxcel-server index models-dir at process start. A reused
	// socket (or weights copied in later) needs a rescan or Load returns 404.
	_ = e.client.ReloadModels(ctx)
	err := e.client.LoadModel(ctx, id)
	if err != nil && !unix.Is(err, unix.CodeAlreadyRunning) {
		return err
	}
	info, err := httpx.WaitUntilLoaded(ctx, e.client, id)
	if err != nil {
		return err
	}
	if info.State == modelstate.ModelFailed {
		return &unix.Error{Code: unix.CodeLoadFailed, Message: "model load failed"}
	}
	logx.Info("frontend model loaded",
		"event", "frontend.model_loaded",
		"kind", e.kind,
		"model_id", id,
	)
	return nil
}

func (e *Engine) Unload(ctx context.Context, id string) error {
	if err := e.Ready(ctx); err != nil {
		return err
	}
	if err := e.client.UnloadModel(ctx, id); err != nil {
		return err
	}
	info, err := httpx.WaitUntilUnloaded(ctx, e.client, id)
	if err != nil {
		return err
	}
	if info.State == modelstate.ModelFailed {
		return &unix.Error{Code: unix.CodeUnloadFailed, Message: "model unload failed"}
	}
	logx.Info("frontend model unloaded",
		"event", "frontend.model_unloaded",
		"kind", e.kind,
		"model_id", id,
	)
	return nil
}

func (e *Engine) Get(ctx context.Context, id string) (unix.Model, error) {
	if err := e.Ready(ctx); err != nil {
		return unix.Model{}, err
	}
	return e.client.GetModel(ctx, id)
}

func (e *Engine) List(ctx context.Context) ([]unix.Model, error) {
	if err := e.Ready(ctx); err != nil {
		return nil, err
	}
	return e.client.ListModels(ctx)
}

func (e *Engine) EnsureReady(ctx context.Context) error {
	if err := e.Ready(ctx); err == nil {
		return nil
	}
	return e.Start(ctx)
}

func (e *Engine) EnsureLoaded(ctx context.Context, id string) error {
	if err := e.EnsureReady(ctx); err != nil {
		return err
	}
	return e.Load(ctx, id)
}

// ModelPID returns the frontend process pid when the named model is loaded.
func (e *Engine) ModelPID(ctx context.Context, id string) (int, error) {
	info, err := e.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if info.State != modelstate.ModelLoaded {
		return 0, &unix.Error{Code: unix.CodeNotRunning, Message: "model not running"}
	}
	pid := e.proc.PID()
	if pid <= 0 {
		return 0, &unix.Error{Code: unix.CodeBackendUnavailable, Message: "frontend process not running"}
	}
	return pid, nil
}

// BackendPIDs returns the single frontend process pid, or empty if not alive.
func (e *Engine) BackendPIDs(ctx context.Context) ([]int, error) {
	_ = ctx
	pid := e.proc.PID()
	if pid <= 0 {
		return nil, nil
	}
	return []int{pid}, nil
}

func (e *Engine) readyLocked(ctx context.Context) error {
	if err := e.client.Healthy(ctx); err != nil {
		return errors.Join(unix.ErrNotReady, err)
	}
	return nil
}

func (e *Engine) waitHealthy(ctx context.Context) error {
	ticker := time.NewTicker(healthPoll)
	defer ticker.Stop()
	var last error
	for {
		if err := e.client.Healthy(ctx); err == nil {
			return nil
		} else {
			last = err
		}
		if !e.proc.Alive() {
			return unix.WrapUnavailable("frontend process exited", last)
		}
		select {
		case <-ctx.Done():
			if last != nil {
				return unix.WrapUnavailable("wait for frontend ready", errors.Join(ctx.Err(), last))
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func withReadyTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, defaultReadyTimeout)
}
