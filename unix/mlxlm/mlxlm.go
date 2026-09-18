// Package mlxlm supervises one mlx-lm Python OS process per loaded model.
// This package is not an Encore service.
package mlxlm

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
	MlxlmAPIKey string
}

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
	ExtraEnv  []string
}

// Runtime is the mlxlm frontend (thin wrapper over supervisor).
type Runtime struct {
	sup *supervisor.Supervisor
	cfg Config
}

// New builds a runtime from Encore secrets and default cwd unix/mlxlm.
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
		sup: supervisor.New("mlxlm"),
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
	modelPath := filepath.Join(r.cfg.ModelsDir, id)
	sock := filepath.Join(r.cfg.Cwd, id+".sock")
	return r.sup.Load(ctx, id, supervisor.SpawnSpec{
		Bin:        r.cfg.Bin,
		Args:       mlxlmArgs(modelPath, sock, r.cfg.APIKey),
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

func (r *Runtime) ChatCompletions(context.Context, http.ResponseWriter, *http.Request) error {
	return unix.ErrNotImplemented
}

func (r *Runtime) Transcribe(context.Context, http.ResponseWriter, *http.Request) error {
	return unix.ErrNotImplemented
}

func applyDefaults(cfg Config) (Config, error) {
	needRoot := cfg.Cwd == "" || cfg.ModelsDir == "" || cfg.Bin == ""
	var root string
	if needRoot {
		var err error
		root, err = proc.AppRoot()
		if err != nil {
			return cfg, err
		}
	}
	if cfg.Bin == "" {
		cfg.Bin = envOr("MLXLM_BIN", defaultBin(root))
	}
	if cfg.Cwd == "" {
		cfg.Cwd = filepath.Join(root, "unix", "mlxlm")
	}
	if cfg.ModelsDir == "" {
		cfg.ModelsDir = envOr("MLXLM_MODELS_DIR", filepath.Join(root, "models"))
	}
	if cfg.APIKey == "" {
		cfg.APIKey = secrets.MlxlmAPIKey
	}
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(os.Getenv("MLXLM_API_KEY"))
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

func defaultBin(root string) string {
	wrapper := filepath.Join(root, "unix", "mlx_lm", "bin", "mlx_lm_server")
	if _, err := os.Stat(wrapper); err == nil {
		return wrapper
	}
	return "mlx_lm_server"
}

func mlxlmArgs(modelPath, host, apiKey string) []string {
	args := []string{
		"--model-path", modelPath,
		"--host", host,
		"--parent-pid", strconv.Itoa(os.Getpid()),
	}
	if apiKey != "" {
		args = append(args, "--api-key", apiKey)
	}
	return args
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
