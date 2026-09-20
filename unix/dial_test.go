package unix

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestDialJSONRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sock := filepath.Join(dir, "t.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req map[string]any
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		_ = json.NewEncoder(conn).Encode(map[string]any{
			"content": "ok",
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := DialForTest(ctx, sock, []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Content != "ok" {
		t.Fatalf("got %q", out.Content)
	}
}
