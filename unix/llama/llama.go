// Package llama talks to llama-server over a Unix socket and can start that process.
// llama-server exposes internal HTTP on the socket, not a public OpenAI port.
// This package is not an Encore service.
package llama

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"encore.app/unix"
	"encore.app/unix/internal/engine"
	"encore.app/unix/internal/httpx"
	"encore.app/unix/internal/proc"
)

var secrets struct {
	LlamaAPIKey string
}

const (
	defaultSocket    = "llama.sock"
	defaultModelsMax = 8
	defaultNGL       = 99
)

var (
	_ unix.Runtime    = (*Runtime)(nil)
	_ unix.Inferencer = (*Runtime)(nil)
)

// Config is passed by tests or by control. Empty fields get defaults.
type Config struct {
	Bin        string
	Cwd        string
	SocketName string
	ModelsDir  string
	APIKey     string
	NGL        int
	ModelsMax  int
	ExtraEnv   []string
}

// Runtime is the llama.cpp frontend.
type Runtime struct {
	*engine.Engine
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

	socketPath := filepath.Join(cfg.Cwd, cfg.SocketName)
	p := proc.New(proc.Config{
		Kind:       "llama",
		Bin:        cfg.Bin,
		Args:       llamaArgs(cfg),
		Cwd:        cfg.Cwd,
		SocketPath: socketPath,
		ExtraEnv:   cfg.ExtraEnv,
	})
	return &Runtime{
		Engine: engine.New("llama", p, httpx.NewUnix(socketPath, cfg.APIKey)),
		cfg:    cfg,
	}, nil
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
	if cfg.SocketName == "" {
		cfg.SocketName = envOr("LLAMA_SOCKET_NAME", defaultSocket)
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
	if cfg.ModelsMax == 0 {
		cfg.ModelsMax = envInt("LLAMA_MODELS_MAX", defaultModelsMax)
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

func llamaArgs(cfg Config) []string {
	return []string{
		"--models-dir", cfg.ModelsDir,
		"--no-models-autoload",
		"--models-max", strconv.Itoa(cfg.ModelsMax),
		"--host", "./" + cfg.SocketName,
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
