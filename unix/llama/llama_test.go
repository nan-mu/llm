package llama

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"encore.app/internal/modelstate"
	"encore.app/unix"
	"encore.app/unix/internal/fakeux"
)

func TestReadyFailsWithoutProcess(t *testing.T) {
	rt := newTestRuntime(t)
	err := rt.Ready(context.Background())
	if err == nil {
		t.Fatal("expected Ready to fail")
	}
	if !errors.Is(err, unix.ErrNotReady) {
		t.Fatalf("Ready error = %v", err)
	}
}

func TestStartReadyLoadGetUnload(t *testing.T) {
	rt := newTestRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(rt.cfg.Cwd, rt.cfg.SocketName)
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket %s: %v", sock, err)
	}
	if err := rt.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	if err := rt.Load(ctx, "tiny"); err != nil {
		t.Fatal(err)
	}
	got, err := rt.Get(ctx, "tiny")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != modelstate.ModelLoaded {
		t.Fatalf("state = %s", got.State)
	}

	if err := rt.Unload(ctx, "tiny"); err != nil {
		t.Fatal(err)
	}
	got, err = rt.Get(ctx, "tiny")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != modelstate.ModelUnloaded {
		t.Fatalf("unloaded state = %s", got.State)
	}
}

func TestEnsureLoaded(t *testing.T) {
	rt := newTestRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := rt.EnsureLoaded(ctx, "tiny"); err != nil {
		t.Fatal(err)
	}
	got, err := rt.Get(ctx, "tiny")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != modelstate.ModelLoaded {
		t.Fatalf("state = %s", got.State)
	}
}

func TestInferencerUnimplemented(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.ChatCompletions(context.Background(), nil, nil); !errors.Is(err, unix.ErrNotImplemented) {
		t.Fatalf("ChatCompletions: %v", err)
	}
}

func TestLlamaArgsDisableAutoload(t *testing.T) {
	cfg := Config{
		ModelsDir:  "/tmp/models",
		SocketName: "llama.sock",
		APIKey:     "k",
		NGL:        99,
		ModelsMax:  8,
	}
	args := llamaArgs(cfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--no-models-autoload") {
		t.Fatalf("missing --no-models-autoload: %v", args)
	}
	for _, a := range args {
		if a == "--model" {
			t.Fatal("argv must not name a model to reside")
		}
	}
}

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	bin, err := fakeux.Build()
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewWithConfig(Config{
		Bin:        bin,
		Cwd:        t.TempDir(),
		SocketName: "llama.sock",
		ModelsDir:  t.TempDir(),
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = rt.Stop(ctx)
	})
	return rt
}
