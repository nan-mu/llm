// Package mlxcel talks to mlxcel-server over a Unix socket and can start that process.
// mlxcel-server exposes internal HTTP on the socket, not a public OpenAI port.
// This package is not an Encore service.
package mlxcel

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"encore.app/frontend"
	"encore.app/frontend/internal/engine"
	"encore.app/frontend/internal/httpx"
	"encore.app/frontend/internal/proc"
)

var secrets struct {
	MlxcelAPIKey string
}

const (
	defaultSocket    = "mlxcel.sock"
	defaultModelsMax = 8
)

var (
	_ frontend.Runtime    = (*Runtime)(nil)
	_ frontend.Inferencer = (*Runtime)(nil)
)

// Config is passed by tests or by control. Empty fields get defaults.
type Config struct {
	Bin            string
	Cwd            string
	SocketName     string
	ModelsDir      string
	ModelStoreRoot string
	APIKey         string
	ModelsMax      int
	ExtraEnv       []string
}

// Runtime is the mlxcel frontend.
type Runtime struct {
	*engine.Engine
	cfg Config
}

// New builds a runtime from Encore secrets and default cwd unix/mlxcel.
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
	if err := os.MkdirAll(cfg.ModelStoreRoot, 0o755); err != nil {
		return nil, err
	}

	socketPath := filepath.Join(cfg.Cwd, cfg.SocketName)
	p := proc.New(proc.Config{
		Kind:       "mlxcel",
		Bin:        cfg.Bin,
		Args:       mlxcelArgs(cfg),
		Cwd:        cfg.Cwd,
		SocketPath: socketPath,
		ExtraEnv:   cfg.ExtraEnv,
	})
	return &Runtime{
		Engine: engine.New("mlxcel", p, httpx.NewUnix(socketPath, cfg.APIKey)),
		cfg:    cfg,
	}, nil
}

func (r *Runtime) ChatCompletions(context.Context, http.ResponseWriter, *http.Request) error {
	return frontend.ErrNotImplemented
}

func (r *Runtime) Transcribe(context.Context, http.ResponseWriter, *http.Request) error {
	return frontend.ErrNotImplemented
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
		cfg.Bin = envOr("MLXCEL_SERVER_BIN", "mlxcel-server")
	}
	if cfg.Cwd == "" {
		cfg.Cwd = filepath.Join(root, "frontend", "mlxcel")
	}
	if cfg.SocketName == "" {
		cfg.SocketName = envOr("MLXCEL_SOCKET_NAME", defaultSocket)
	}
	if cfg.ModelsDir == "" {
		cfg.ModelsDir = envOr("MLXCEL_MODELS_DIR", filepath.Join(root, "models"))
	}
	if cfg.ModelStoreRoot == "" {
		cfg.ModelStoreRoot = envOr("MLXCEL_MODEL_STORE_ROOT", filepath.Join(cfg.Cwd, "store"))
	}
	if cfg.APIKey == "" {
		cfg.APIKey = secrets.MlxcelAPIKey
	}
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(os.Getenv("MLXCEL_API_KEY"))
	}
	if cfg.ModelsMax == 0 {
		cfg.ModelsMax = envInt("MLXCEL_MODELS_MAX", defaultModelsMax)
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
	if !filepath.IsAbs(cfg.ModelStoreRoot) {
		cfg.ModelStoreRoot = filepath.Join(cfg.Cwd, cfg.ModelStoreRoot)
	}
	cfg.ModelStoreRoot, err = filepath.Abs(cfg.ModelStoreRoot)
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

func mlxcelArgs(cfg Config) []string {
	return []string{
		"--models-dir", cfg.ModelsDir,
		"--no-models-autoload",
		"--models-max", strconv.Itoa(cfg.ModelsMax),
		"--model-store-root", cfg.ModelStoreRoot,
		"--host", "./" + cfg.SocketName,
		"--port", "0",
		"--api-key", cfg.APIKey,
		"--sleep-idle-seconds", "-1",
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
