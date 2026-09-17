// Package fakeux is a tiny HTTP-over-UDS frontend used as a child process in tests.
package fakeux

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Main serves HTTP-over-UDS using --host and --api-key from argv. It blocks.
func Main() {
	host := flagValue(os.Args[1:], "--host")
	if host == "" {
		host = "./llama.sock"
	}
	apiKey := flagValue(os.Args[1:], "--api-key")
	_ = os.Remove(host)
	ln, err := net.Listen("unix", host)
	if err != nil {
		panic(err)
	}
	if err := http.Serve(ln, newMux(apiKey)); err != nil {
		panic(err)
	}
}

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

// Build compiles the encore-free fake frontend binary for tests.
func Build() (string, error) {
	buildOnce.Do(func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			buildErr = fmt.Errorf("fakeux: cannot locate source")
			return
		}
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
		dir, err := os.MkdirTemp("", "fakeux-bin-")
		if err != nil {
			buildErr = err
			return
		}
		builtBin = filepath.Join(dir, "fakeux")
		cmd := exec.Command("go", "build", "-o", builtBin, "./unix/internal/fakeux/cmd/fakeux")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build fakeux: %w\n%s", err, out)
		}
	})
	return builtBin, buildErr
}

type model struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Status struct {
		Value string `json:"value"`
	} `json:"status"`
}

func newMux(apiKey string) http.Handler {
	var mu sync.Mutex
	models := map[string]*model{}

	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if apiKey == "" {
			return true
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
			return false
		}
		return true
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/models/load", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" {
			http.Error(w, `{"error":{"message":"model is not found"}}`, http.StatusNotFound)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if m, ok := models[body.Model]; ok && m.Status.Value == "loaded" {
			http.Error(w, `{"error":{"message":"model is already running"}}`, http.StatusBadRequest)
			return
		}
		m := &model{ID: body.Model, Path: "/models/" + body.Model}
		m.Status.Value = "loaded"
		models[body.Model] = m
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	mux.HandleFunc("/models/unload", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		if m, ok := models[body.Model]; ok {
			m.Status.Value = "unloaded"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		data := make([]*model, 0, len(models))
		for _, m := range models {
			data = append(data, m)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	return mux
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
