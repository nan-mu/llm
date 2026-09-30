// Package supervisor manages one child OS process per loaded model.
// Unlike engine (single long-lived frontend + HTTP load/unload), Load spawns
// a process and Unload kills it.
package supervisor

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

// SpawnSpec describes how to launch one model worker.
type SpawnSpec struct {
	Bin        string
	Args       []string
	Cwd        string
	SocketPath string
	ExtraEnv   []string
	APIKey     string
	// Path is recorded on Get/List (usually the weights path).
	Path string
	// JSONProtocol: worker speaks one-shot JSON-over-UDS (mlxlm), not HTTP.
	JSONProtocol bool
}

type entry struct {
	proc   *proc.Proc
	client *httpx.Client
	path   string
	sock   string
	json   bool
}

// ExitHandler is called when a worker exits unexpectedly (not via Unload).
// id is the model id that was being served; exitCode is from the child process
// (-1 if unknown).
type ExitHandler func(id string, exitCode int)

// Supervisor is an in-memory table of per-model child processes.
type Supervisor struct {
	kind        string
	mu          sync.Mutex
	ready       bool
	byID        map[string]*entry
	exitHandler ExitHandler
}

// New returns a supervisor that has not been Start'ed yet.
func New(kind string) *Supervisor {
	return &Supervisor{
		kind: kind,
		byID: make(map[string]*entry),
	}
}

// Start marks the supervisor ready. It does not spawn model workers.
func (s *Supervisor) Start(ctx context.Context) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = true
	logx.Info("frontend ready",
		"event", "frontend.ready",
		"kind", s.kind,
	)
	return nil
}

// Stop kills every child and clears ready.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	entries := make(map[string]*entry, len(s.byID))
	for id, e := range s.byID {
		entries[id] = e
		delete(s.byID, id)
	}
	s.ready = false
	s.mu.Unlock()

	var first error
	for _, e := range entries {
		if err := e.proc.Stop(ctx); err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		return unix.WrapUnavailable("stop frontend", first)
	}
	return nil
}

// Ready reports whether Start has been called and not yet Stop'ed.
func (s *Supervisor) Ready(ctx context.Context) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return unix.ErrNotReady
	}
	return nil
}

// SetExitHandler registers a callback for unexpected worker death.
// Unload deletes the entry before Stop, so intentional stops do not fire it.
func (s *Supervisor) SetExitHandler(fn ExitHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exitHandler = fn
}

// Load spawns a worker for id (or returns if already healthy).
func (s *Supervisor) Load(ctx context.Context, id string, spec SpawnSpec) error {
	if err := s.Ready(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	if e, ok := s.byID[id]; ok {
		if e.proc.Alive() {
			if err := entryHealthy(ctx, e); err == nil {
				s.mu.Unlock()
				return nil
			}
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = e.proc.Stop(stopCtx)
		cancel()
		delete(s.byID, id)
	}
	s.mu.Unlock()

	waitCtx, cancel := withReadyTimeout(ctx)
	defer cancel()

	p := proc.New(proc.Config{
		Kind:       s.kind,
		Bin:        spec.Bin,
		Args:       spec.Args,
		Cwd:        spec.Cwd,
		SocketPath: spec.SocketPath,
		ExtraEnv:   spec.ExtraEnv,
	})
	client := httpx.NewUnix(spec.SocketPath, spec.APIKey)
	ent := &entry{proc: p, client: client, path: spec.Path, sock: spec.SocketPath, json: spec.JSONProtocol}

	if err := p.Start(waitCtx); err != nil {
		return unix.WrapUnavailable("start model worker", err)
	}
	if err := waitHealthy(waitCtx, ent); err != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = p.Stop(stopCtx)
		stopCancel()
		return &unix.Error{Code: unix.CodeLoadFailed, Message: err.Error(), Err: err}
	}

	s.mu.Lock()
	s.byID[id] = ent
	s.mu.Unlock()

	s.watchExit(id, p)

	logx.Info("frontend model loaded",
		"event", "frontend.model_loaded",
		"kind", s.kind,
		"model_id", id,
	)
	return nil
}

// watchExit fires exitHandler once if the process dies while still registered.
func (s *Supervisor) watchExit(id string, p *proc.Proc) {
	done := p.Done()
	if done == nil {
		return
	}
	go func() {
		<-done
		s.mu.Lock()
		e, ok := s.byID[id]
		if !ok || e.proc != p {
			s.mu.Unlock()
			return
		}
		delete(s.byID, id)
		handler := s.exitHandler
		s.mu.Unlock()

		exitCode := p.ExitCode()
		logx.Warn("frontend worker exited",
			"event", "frontend.worker_exited",
			"kind", s.kind,
			"model_id", id,
			"exit_code", exitCode,
		)
		if handler != nil {
			handler(id, exitCode)
		}
	}()
}

// Unload stops the worker for id.
func (s *Supervisor) Unload(ctx context.Context, id string) error {
	if err := s.Ready(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	e, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return &unix.Error{Code: unix.CodeNotFound, Message: "model not found"}
	}
	delete(s.byID, id)
	s.mu.Unlock()

	if err := e.proc.Stop(ctx); err != nil {
		return &unix.Error{Code: unix.CodeUnloadFailed, Message: err.Error(), Err: err}
	}
	logx.Info("frontend model unloaded",
		"event", "frontend.model_unloaded",
		"kind", s.kind,
		"model_id", id,
	)
	return nil
}

// Get returns residency for one model.
func (s *Supervisor) Get(ctx context.Context, id string) (unix.Model, error) {
	if err := s.Ready(ctx); err != nil {
		return unix.Model{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.byID[id]
	if !ok {
		return unix.Model{}, &unix.Error{Code: unix.CodeNotFound, Message: "model not found"}
	}
	return e.toModel(id), nil
}

// List returns all tracked models.
func (s *Supervisor) List(ctx context.Context) ([]unix.Model, error) {
	if err := s.Ready(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]unix.Model, 0, len(s.byID))
	for id, e := range s.byID {
		out = append(out, e.toModel(id))
	}
	return out, nil
}

// EnsureReady starts the supervisor if needed.
func (s *Supervisor) EnsureReady(ctx context.Context) error {
	if err := s.Ready(ctx); err == nil {
		return nil
	}
	return s.Start(ctx)
}

// PID returns the worker pid for id, or false if missing/dead.
func (s *Supervisor) PID(id string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.byID[id]
	if !ok {
		return 0, false
	}
	pid := e.proc.PID()
	if pid <= 0 {
		return 0, false
	}
	return pid, true
}

// PIDs returns all live worker pids (skips dead children).
func (s *Supervisor) PIDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int, 0, len(s.byID))
	for id, e := range s.byID {
		if !e.proc.Alive() {
			delete(s.byID, id)
			continue
		}
		if pid := e.proc.PID(); pid > 0 {
			out = append(out, pid)
		}
	}
	return out
}

// Socket returns the Unix socket path for a loaded model id.
func (s *Supervisor) Socket(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.byID[id]
	if !ok || e.sock == "" {
		return "", false
	}
	return e.sock, true
}

func (e *entry) toModel(id string) unix.Model {
	state := modelstate.ModelLoaded
	if !e.proc.Alive() {
		state = modelstate.ModelFailed
	}
	return unix.Model{ID: id, Path: e.path, State: state}
}

func entryHealthy(ctx context.Context, e *entry) error {
	if e.json {
		return httpx.PingJSON(ctx, e.sock)
	}
	return e.client.Healthy(ctx)
}

func waitHealthy(ctx context.Context, e *entry) error {
	ticker := time.NewTicker(healthPoll)
	defer ticker.Stop()
	var last error
	for {
		if err := entryHealthy(ctx, e); err == nil {
			return nil
		} else {
			last = err
		}
		if !e.proc.Alive() {
			return unix.WrapUnavailable("model worker exited", last)
		}
		select {
		case <-ctx.Done():
			if last != nil {
				return unix.WrapUnavailable("wait for model worker ready", errors.Join(ctx.Err(), last))
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
