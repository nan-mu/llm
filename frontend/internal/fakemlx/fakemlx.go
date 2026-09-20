// Package fakemlx is a JSON-over-UDS child used in mlxlm/supervisor tests.
package fakemlx

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Main serves one-shot JSON requests on a Unix socket from --host.
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
	for {
		conn, err := ln.Accept()
		if err != nil {
			panic(err)
		}
		go serve(conn)
	}
}

func serve(conn net.Conn) {
	defer conn.Close()
	raw, _ := io.ReadAll(conn)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		_, _ = conn.Write([]byte(`{"error":{"message":"invalid json","type":"invalid_request_error"}}`))
		return
	}
	if op, _ := body["op"].(string); op == "health" {
		_, _ = conn.Write([]byte(`{"ok":true}`))
		return
	}
	if ping, _ := body["ping"].(bool); ping {
		_, _ = conn.Write([]byte(`{"ok":true}`))
		return
	}
	resp := map[string]any{
		"content": "fakemlx-ok",
		"usage": map[string]int{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}
	_ = json.NewEncoder(conn).Encode(resp)
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
		cmd := exec.Command("go", "build", "-o", builtBin, "./frontend/internal/fakemlx/cmd/fakemlx")
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
