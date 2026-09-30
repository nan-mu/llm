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

func TestReadyFailsWithoutStart(t *testing.T) {
	rt := newTestRuntime(t)
	err := rt.Ready(context.Background())
	if err == nil {
		t.Fatal("expected Ready to fail")
	}
	if !errors.Is(err, unix.ErrNotReady) {
		t.Fatalf("Ready error = %v", err)
	}
}

func TestStartReadyWithNoSock(t *testing.T) {
	rt := newTestRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(rt.cfg.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sock") {
			t.Fatalf("unexpected sock after Start: %s", e.Name())
		}
	}
}

func TestStartReadyLoadTwoUnloadOne(t *testing.T) {
	rt := newTestRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	if err := rt.Load(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := rt.Load(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	sockA := filepath.Join(rt.cfg.Cwd, "alpha.sock")
	sockB := filepath.Join(rt.cfg.Cwd, "beta.sock")
	if _, err := os.Stat(sockA); err != nil {
		t.Fatalf("alpha sock: %v", err)
	}
	if _, err := os.Stat(sockB); err != nil {
		t.Fatalf("beta sock: %v", err)
	}

	got, err := rt.Get(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != modelstate.ModelLoaded {
		t.Fatalf("state = %s", got.State)
	}
	wantPath := filepath.Join(rt.cfg.ModelsDir, "alpha.gguf")
	if got.Path != wantPath {
		t.Fatalf("path = %s, want %s", got.Path, wantPath)
	}

	if err := rt.Unload(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sockA); !os.IsNotExist(err) {
		t.Fatalf("alpha sock should be gone")
	}
	if _, err := os.Stat(sockB); err != nil {
		t.Fatalf("beta sock should remain: %v", err)
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
	sock := filepath.Join(rt.cfg.Cwd, "tiny.sock")
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("sock: %v", err)
	}
}

func TestInferencerUnimplemented(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.ChatCompletions(context.Background(), nil, nil); !errors.Is(err, unix.ErrNotImplemented) {
		t.Fatalf("ChatCompletions: %v", err)
	}
	if err := rt.Transcribe(context.Background(), nil, nil); !errors.Is(err, unix.ErrNotImplemented) {
		t.Fatalf("Transcribe: %v", err)
	}
}

func TestLlamaWorkerArgs(t *testing.T) {
	cfg := Config{APIKey: "k", NGL: 99}
	args := llamaWorkerArgs("/tmp/models/foo.gguf", "foo", cfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-m /tmp/models/foo.gguf") {
		t.Fatalf("missing -m: %v", args)
	}
	if !strings.Contains(joined, "--host ./foo.sock") {
		t.Fatalf("missing relative host: %v", args)
	}
	if !strings.Contains(joined, "--models-max 1") {
		t.Fatalf("missing models-max 1: %v", args)
	}
	for _, a := range args {
		if a == "--port" || a == "127.0.0.1" || strings.HasPrefix(a, "0.0.0.0") {
			t.Fatalf("must not bind TCP: %v", args)
		}
		if a == "llama.sock" || strings.Contains(a, "llama.sock") {
			t.Fatalf("must not use shared llama.sock: %v", args)
		}
	}
}

func TestStopClearsChildren(t *testing.T) {
	rt := newTestRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.EnsureLoaded(ctx, "z"); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(rt.cfg.Cwd, "z.sock")
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("sock should be removed after Stop")
	}
	if err := rt.Ready(ctx); err == nil {
		t.Fatal("Ready should fail after Stop")
	}
}

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	bin, err := fakeux.Build()
	if err != nil {
		t.Fatal(err)
	}
	// macOS AF_UNIX paths cap at 104 bytes; t.TempDir() plus the test name overflows.
	cwd, err := os.MkdirTemp("", "ll-")
	if err != nil {
		t.Fatal(err)
	}
	models, err := os.MkdirTemp("", "lm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(cwd)
		_ = os.RemoveAll(models)
	})
	rt, err := NewWithConfig(Config{
		Bin:       bin,
		Cwd:       cwd,
		ModelsDir: models,
		APIKey:    "test-key",
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
