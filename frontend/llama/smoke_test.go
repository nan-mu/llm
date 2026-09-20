package llama

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"encore.app/internal/modelstate"
)

func TestSmokeLlamaServer(t *testing.T) {
	id := os.Getenv("LLAMA_SMOKE_MODEL")
	if id == "" {
		t.Skip("set LLAMA_SMOKE_MODEL to a small GGUF id to smoke llama-server")
	}
	bin := os.Getenv("LLAMA_SERVER_BIN")
	if bin == "" {
		bin = "llama-server"
	}
	if _, err := exec.LookPath(bin); err != nil {
		if _, statErr := os.Stat(bin); statErr != nil {
			t.Skip("llama-server not found")
		}
	}

	cwd, err := os.MkdirTemp("", "ll-smoke-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cwd) })

	rt, err := NewWithConfig(Config{
		Bin:    bin,
		Cwd:    cwd,
		APIKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	gguf := filepath.Join(rt.cfg.ModelsDir, id+".gguf")
	if _, err := os.Stat(gguf); err != nil {
		t.Skipf("GGUF not found at %s: %v", gguf, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = rt.Stop(stopCtx)
	})

	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Load(ctx, id); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(rt.cfg.Cwd, id+".sock")
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("sock %s: %v", sock, err)
	}
	got, err := rt.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != modelstate.ModelLoaded {
		t.Fatalf("state = %s", got.State)
	}
}
