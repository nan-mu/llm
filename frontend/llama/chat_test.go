package llama

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatProxyPostChatCompletions(t *testing.T) {
	// macOS sun_path is short; keep the whole sock path under ~100 bytes.
	dir, err := os.MkdirTemp("/tmp", "llchat-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "m.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	})
	go func() { _ = http.Serve(ln, mux) }()

	proxy, err := NewChatProxyWithConfig(Config{
		Cwd:       dir,
		APIKey:    "test-key",
		ModelsDir: dir,
		Bin:       "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, raw, err := proxy.PostChatCompletions(ctx, "m", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), `"ok"`) {
		t.Fatalf("body=%s", raw)
	}
}
