package mlxcel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"encore.app/internal/modelstate"
	"encore.app/unix"
	"encore.app/unix/internal/fakeux"
)

func TestStartReadyLoad(t *testing.T) {
	rt := newTestRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := rt.Ready(ctx); err == nil {
		t.Fatal("expected Ready to fail without process")
	}

	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
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
}

func TestInferencerUnimplemented(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.Transcribe(context.Background(), nil, nil); !errors.Is(err, unix.ErrNotImplemented) {
		t.Fatalf("Transcribe: %v", err)
	}
}

func TestMlxcelArgs(t *testing.T) {
	args := mlxcelArgs(Config{
		ModelsDir:      "/tmp/models",
		ModelStoreRoot: "/tmp/store",
		SocketName:     "mlxcel.sock",
		APIKey:         "k",
		ModelsMax:      8,
	})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--no-models-autoload", "--port 0", "--sleep-idle-seconds -1", "--model-store-root"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, args)
		}
	}
}

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	bin, err := fakeux.Build()
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	rt, err := NewWithConfig(Config{
		Bin:            bin,
		Cwd:            cwd,
		SocketName:     "mlxcel.sock",
		ModelsDir:      t.TempDir(),
		ModelStoreRoot: cwd + "/store",
		APIKey:         "test-key",
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
