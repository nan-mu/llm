// Package unix is the private infer router. It dials mlxlm JSON-over-UDS and does not Start or Load models.
package unix

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"encore.app/internal/approot"
	"encore.dev/beta/errs"
	"encore.dev/rlog"
)

//encore:service
type Service struct {
	cwd string
}

func initService() (*Service, error) {
	root, err := approot.Root()
	if err != nil {
		return nil, err
	}
	cwd := filepath.Join(root, "frontend", "mlxlm")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	return &Service{cwd: abs}, nil
}

// ChatBody is opaque prompt JSON. The body field is the Encore service-to-service wrapper.
type ChatBody struct {
	Body json.RawMessage `json:"body"`
}

// ChatResult is the opaque worker JSON.
type ChatResult struct {
	Body json.RawMessage `json:"body"`
}

// Chat forwards one JSON document to frontend/mlxlm/{native_id}.sock.
// One connection is one request and one response. There is no middleware on this endpoint.
//
//encore:api private method=POST path=/unix/chat/:native_id
func (s *Service) Chat(ctx context.Context, native_id string, p *ChatBody) (*ChatResult, error) {
	id, err := safeNativeID(native_id)
	if err != nil {
		return nil, err
	}
	var body json.RawMessage
	if p != nil {
		body = p.Body
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte("{}")
	}
	sock := filepath.Join(s.cwd, id+".sock")
	resp, err := dialJSON(ctx, sock, body)
	if err != nil {
		rlog.Error("unix chat dial failed", "native_id", id, "err", err)
		return nil, &errs.Error{Code: errs.Unavailable, Message: "backend unavailable"}
	}
	return &ChatResult{Body: json.RawMessage(resp)}, nil
}

func safeNativeID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, `\`) || strings.Contains(id, "..") {
		return "", &errs.Error{Code: errs.InvalidArgument, Message: "model required"}
	}
	return id, nil
}
