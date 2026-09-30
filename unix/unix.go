package unix

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"encore.dev/beta/errs"
	"encore.dev/rlog"
)

//encore:service
type Service struct {
	cwd string
}

func initService() (*Service, error) {
	root, err := appRoot()
	if err != nil {
		return nil, err
	}
	cwd := filepath.Join(root, "unix", "mlxlm")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	return &Service{cwd: abs}, nil
}

func appRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "encore.app")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("encore.app not found from %s", dir)
		}
		dir = parent
	}
}

// ChatBody is opaque prompt JSON for the worker (Encore wraps S2S calls).
type ChatBody struct {
	Body json.RawMessage `json:"body"`
}

// ChatResult is the opaque worker JSON (content/usage or error).
type ChatResult struct {
	Body json.RawMessage `json:"body"`
}

// Chat forwards opaque prompt JSON to the mlxlm worker. No middleware; no schema.
//
//encore:api private method=POST path=/unix/chat/:native_id
func (s *Service) Chat(ctx context.Context, native_id string, p *ChatBody) (*ChatResult, error) {
	if strings.TrimSpace(native_id) == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "model required"}
	}
	var body json.RawMessage
	if p != nil {
		body = p.Body
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte("{}")
	}
	sock := filepath.Join(s.cwd, strings.TrimSpace(native_id)+".sock")
	resp, err := dialJSON(ctx, sock, body)
	if err != nil {
		rlog.Error("unix chat dial failed", "event", "unix.chat_failed", "native_id", native_id, "err", err)
		return nil, &errs.Error{Code: errs.Unavailable, Message: "backend unavailable"}
	}
	return &ChatResult{Body: json.RawMessage(resp)}, nil
}

// SocketPath is exported for tests.
func (s *Service) SocketPath(nativeID string) string {
	return filepath.Join(s.cwd, nativeID+".sock")
}

// DialForTest exposes dialJSON for unit tests.
func DialForTest(ctx context.Context, socket string, body []byte) ([]byte, error) {
	return dialJSON(ctx, socket, body)
}
