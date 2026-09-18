package mlxlm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"encore.app/internal/modelstate"
	"encore.app/unix"
	"encore.app/unix/internal/fakemlx"
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
	wantPath := filepath.Join(rt.cfg.ModelsDir, "alpha")
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

func TestTranscribeUnimplemented(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.Transcribe(context.Background(), nil, nil); !errors.Is(err, unix.ErrNotImplemented) {
		t.Fatalf("Transcribe: %v", err)
	}
}

func TestChatCompletionsProxy(t *testing.T) {
	rt := newTestRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.EnsureLoaded(ctx, "chatm"); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"chatm","messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	if err := rt.ChatCompletions(ctx, rr, req); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "fakemlx-ok") {
		t.Fatalf("body = %s", rr.Body.String())
	}

	proxy, err := NewChatProxyWithConfig(Config{Cwd: rt.cfg.Cwd, APIKey: rt.cfg.APIKey, ModelsDir: rt.cfg.ModelsDir, Bin: rt.cfg.Bin})
	if err != nil {
		t.Fatal(err)
	}
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	if err := proxy.ChatCompletions(ctx, rr2, req2); err != nil {
		t.Fatal(err)
	}
	if rr2.Code != http.StatusOK {
		t.Fatalf("proxy status = %d body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestMlxlmArgs(t *testing.T) {
	args := mlxlmArgs("/models/g", "/tmp/g.sock", "secret")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--model-path /models/g") {
		t.Fatalf("args = %v", args)
	}
	if !strings.Contains(joined, "--host /tmp/g.sock") {
		t.Fatalf("args = %v", args)
	}
	if !strings.Contains(joined, "--api-key secret") {
		t.Fatalf("args = %v", args)
	}
	if !strings.Contains(joined, "--parent-pid") {
		t.Fatalf("missing --parent-pid: %v", args)
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
	bin, err := fakemlx.Build()
	if err != nil {
		t.Fatal(err)
	}
	// macOS AF_UNIX paths cap at 104 bytes; t.TempDir() plus the test name overflows.
	cwd, err := os.MkdirTemp("", "ml-")
	if err != nil {
		t.Fatal(err)
	}
	models, err := os.MkdirTemp("", "mm-")
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
		_ = rt.Stop(context.Background())
	})
	return rt
}
