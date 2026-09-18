// Package fakemlx is a health-only HTTP-over-UDS child used in mlxlm/supervisor tests.
// It does not implement /models/load or /models/unload.
package fakemlx

import (
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

// Main serves GET /health on a Unix socket from --host. It blocks.
func Main() {
	host := flagValue(os.Args[1:], "--host")
	if host == "" {
		host = "./mlxlm.sock"
	}
	_ = os.Remove(host)
	ln, err := net.Listen("unix", host)
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
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
