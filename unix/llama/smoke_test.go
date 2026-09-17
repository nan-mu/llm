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

	rt, err := NewWithConfig(Config{
		Bin:        bin,
		Cwd:        t.TempDir(),
		SocketName: "llama.sock",
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatal(err)
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
	if _, err := os.Stat(filepath.Join(rt.cfg.Cwd, rt.cfg.SocketName)); err != nil {
		t.Fatal(err)
	}
	if err := rt.Load(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := rt.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != modelstate.ModelLoaded {
		t.Fatalf("state = %s", got.State)
	}
}
