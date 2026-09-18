// Package fakemlx is a health+chat HTTP-over-UDS child used in mlxlm/supervisor tests.
// It does not implement /models/load or /models/unload.
package fakemlx

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Main serves GET /health and POST /v1/chat/completions on a Unix socket from --host.
func Main() {
	host := flagValue(os.Args[1:], "--host")
	if host == "" {
		host = "./mlxlm.sock"
	}
	apiKey := flagValue(os.Args[1:], "--api-key")
	_ = os.Remove(host)
	ln, err := net.Listen("unix", host)
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if apiKey != "" {
			want := "Bearer " + apiKey
			if r.Header.Get("Authorization") != want {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error"}}`))
				return
			}
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &body)
		model := body.Model
		if model == "" {
			model = "fakemlx"
		}
		resp := map[string]any{
			"id":      "chatcmpl-fakemlx",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]string{
						"role":    "assistant",
						"content": "fakemlx-ok",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     0,
				"completion_tokens": 0,
				"total_tokens":      0,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	if err := http.Serve(ln, mux); err != nil {
		panic(err)
	}
}

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

// Build compiles the encore-free fakemlx binary for tests.
func Build() (string, error) {
	buildOnce.Do(func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			buildErr = fmt.Errorf("fakemlx: cannot locate source")
			return
		}
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
		dir, err := os.MkdirTemp("", "fakemlx-bin-")
		if err != nil {
			buildErr = err
			return
		}
		builtBin = filepath.Join(dir, "fakemlx")
		cmd := exec.Command("go", "build", "-o", builtBin, "./unix/internal/fakemlx/cmd/fakemlx")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build fakemlx: %w\n%s", err, out)
		}
	})
	return builtBin, buildErr
}

func flagValue(args []string, name string) string {
	prefix := name + "="
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix)
		}
	}
	return ""
}
