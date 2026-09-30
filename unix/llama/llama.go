// Package llama supervises one llama-server OS process per loaded GGUF model.
// Each worker listens on a Unix socket only (no TCP). This package is not an Encore service.
package llama

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"encore.app/unix"
	"encore.app/unix/internal/proc"
	"encore.app/unix/internal/supervisor"
)

var secrets struct {
	LlamaAPIKey string
}

const defaultNGL = 99

var (
	_ unix.Runtime    = (*Runtime)(nil)
	_ unix.Inferencer = (*Runtime)(nil)
)

// Config is passed by tests or by control. Empty fields get defaults.
type Config struct {
	Bin       string
	Cwd       string
	ModelsDir string
	APIKey    string
	NGL       int
	ExtraEnv  []string
}

// Runtime is the llama.cpp frontend (thin wrapper over supervisor).
type Runtime struct {
	sup *supervisor.Supervisor
	cfg Config
}

// New builds a runtime from Encore secrets and default cwd unix/llama.
func New() (*Runtime, error) {
	return NewWithConfig(Config{})
}

// NewWithConfig builds a runtime. Used by tests to inject a fake binary.
func NewWithConfig(cfg Config) (*Runtime, error) {
	cfg, err := applyDefaults(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Cwd, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.ModelsDir, 0o755); err != nil {
		return nil, err
	}
	return &Runtime{
		sup: supervisor.New("llama"),
		cfg: cfg,
	}, nil
}

func (r *Runtime) Start(ctx context.Context) error {
	return r.sup.Start(ctx)
}

func (r *Runtime) Stop(ctx context.Context) error {
	return r.sup.Stop(ctx)
}

func (r *Runtime) Ready(ctx context.Context) error {
	return r.sup.Ready(ctx)
}

func (r *Runtime) Load(ctx context.Context, id string) error {
	modelPath := filepath.Join(r.cfg.ModelsDir, id+".gguf")
	sock := filepath.Join(r.cfg.Cwd, id+".sock")
	return r.sup.Load(ctx, id, supervisor.SpawnSpec{
		Bin:        r.cfg.Bin,
		Args:       llamaWorkerArgs(modelPath, id, r.cfg),
		Cwd:        r.cfg.Cwd,
		SocketPath: sock,
		ExtraEnv:   r.cfg.ExtraEnv,
		APIKey:     r.cfg.APIKey,
		Path:       modelPath,
	})
}

func (r *Runtime) Unload(ctx context.Context, id string) error {
	return r.sup.Unload(ctx, id)
}

func (r *Runtime) Get(ctx context.Context, id string) (unix.Model, error) {
	return r.sup.Get(ctx, id)
}

func (r *Runtime) List(ctx context.Context) ([]unix.Model, error) {
	return r.sup.List(ctx)
}

func (r *Runtime) EnsureReady(ctx context.Context) error {
	return r.sup.EnsureReady(ctx)
}

func (r *Runtime) EnsureLoaded(ctx context.Context, id string) error {
	if err := r.EnsureReady(ctx); err != nil {
		return err
	}
	return r.Load(ctx, id)
}

func (r *Runtime) ModelPID(ctx context.Context, id string) (int, error) {
	if err := r.Ready(ctx); err != nil {
		return 0, err
	}
	pid, ok := r.sup.PID(id)
	if !ok {
		return 0, &unix.Error{Code: unix.CodeNotFound, Message: "model not found"}
	}
	return pid, nil
}

func (r *Runtime) BackendPIDs(ctx context.Context) ([]int, error) {
	if err := r.Ready(ctx); err != nil {
		return nil, nil
	}
	return r.sup.PIDs(), nil
}

// SetWorkerExitHandler registers a callback for unexpected worker process death.
// exitCode is the child process exit status (-1 if unknown).
func (r *Runtime) SetWorkerExitHandler(fn func(id string, exitCode int)) {
	r.sup.SetExitHandler(fn)
}

func (r *Runtime) ChatCompletions(context.Context, http.ResponseWriter, *http.Request) error {
	return unix.ErrNotImplemented
}

func (r *Runtime) Transcribe(context.Context, http.ResponseWriter, *http.Request) error {
	return unix.ErrNotImplemented
}

func applyDefaults(cfg Config) (Config, error) {
	needRoot := cfg.Cwd == "" || cfg.ModelsDir == ""
	var root string
	if needRoot {
		var err error
		root, err = proc.AppRoot()
		if err != nil {
			return cfg, err
		}
	}
	if cfg.Bin == "" {
		cfg.Bin = envOr("LLAMA_SERVER_BIN", "llama-server")
	}
	if cfg.Cwd == "" {
		cfg.Cwd = filepath.Join(root, "unix", "llama")
	}
	if cfg.ModelsDir == "" {
		cfg.ModelsDir = envOr("LLAMA_MODELS_DIR", filepath.Join(root, "models"))
	}
	if cfg.APIKey == "" {
		cfg.APIKey = secrets.LlamaAPIKey
	}
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(os.Getenv("LLAMA_API_KEY"))
	}
	if cfg.NGL == 0 {
		cfg.NGL = envInt("LLAMA_NGL", defaultNGL)
	}
	var err error
	cfg.Cwd, err = filepath.Abs(cfg.Cwd)
	if err != nil {
		return cfg, err
	}
	cfg.ModelsDir, err = filepath.Abs(cfg.ModelsDir)
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

// llamaWorkerArgs builds argv for one llama-server process pinned to a single GGUF.
// Host is a cwd-relative UDS path; never bind TCP.
func llamaWorkerArgs(modelPath, id string, cfg Config) []string {
	return []string{
		"-m", modelPath,
		"--models-max", "1",
		"--host", "./" + id + ".sock",
		"--api-key", cfg.APIKey,
		"--metrics",
		"--jinja",
		"-ngl", strconv.Itoa(cfg.NGL),
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
