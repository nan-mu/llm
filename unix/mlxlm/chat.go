package mlxlm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"encore.app/unix"
)

// ChatProxy is a dial-only Inferencer (JSON-over-UDS). It does not Start/Load processes.
type ChatProxy struct {
	Cwd    string
	APIKey string
}

var _ unix.Inferencer = (*ChatProxy)(nil)

// NewChatProxy builds a gateway-plane proxy with the same defaults as Runtime.
func NewChatProxy() (*ChatProxy, error) {
	cfg, err := applyDefaults(Config{})
	if err != nil {
		return nil, err
	}
	return &ChatProxy{Cwd: cfg.Cwd, APIKey: cfg.APIKey}, nil
}

// NewChatProxyWithConfig is for tests.
func NewChatProxyWithConfig(cfg Config) (*ChatProxy, error) {
	cfg, err := applyDefaults(cfg)
	if err != nil {
		return nil, err
	}
	return &ChatProxy{Cwd: cfg.Cwd, APIKey: cfg.APIKey}, nil
}

// SocketPath returns the UDS path for a native model id.
func (p *ChatProxy) SocketPath(nativeID string) string {
	return filepath.Join(p.Cwd, nativeID+".sock")
}

func (p *ChatProxy) ChatCompletions(ctx context.Context, w http.ResponseWriter, req *http.Request) error {
	nativeID, body, err := rewriteModelFromBody(req)
	if err != nil {
		http.Error(w, `{"error":{"message":"invalid request body","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return nil
	}
	if nativeID == "" {
		http.Error(w, `{"error":{"message":"model required","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return nil
	}
	status, raw, err := p.PostChatCompletions(ctx, nativeID, body, nil)
	if err != nil {
		http.Error(w, `{"error":{"message":"backend unavailable","type":"server_error"}}`, http.StatusBadGateway)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
	return nil
}

// PostChatCompletions dials the model sock with one-shot JSON (not HTTP).
func (p *ChatProxy) PostChatCompletions(ctx context.Context, nativeID string, body []byte, _ http.Header) (status int, resp []byte, err error) {
	nativeID = strings.TrimSpace(nativeID)
	if nativeID == "" {
		return http.StatusBadRequest, nil, &unix.Error{Code: unix.CodeNotFound, Message: "model required"}
	}
	raw, err := dialJSON(ctx, p.SocketPath(nativeID), body)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	return http.StatusOK, raw, nil
}

func (p *ChatProxy) Transcribe(context.Context, http.ResponseWriter, *http.Request) error {
	return unix.ErrNotImplemented
}

func (r *Runtime) ChatCompletions(ctx context.Context, w http.ResponseWriter, req *http.Request) error {
	nativeID, body, err := rewriteModelFromBody(req)
	if err != nil {
		http.Error(w, `{"error":{"message":"invalid request body","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return nil
	}
	sock, ok := r.sup.Socket(nativeID)
	if !ok {
		sock = filepath.Join(r.cfg.Cwd, nativeID+".sock")
		if _, err := os.Stat(sock); err != nil {
			http.Error(w, `{"error":{"message":"model not loaded","type":"server_error","code":"model_not_loaded"}}`, http.StatusServiceUnavailable)
			return nil
		}
	}
	raw, err := dialJSON(ctx, sock, body)
	if err != nil {
		http.Error(w, `{"error":{"message":"backend unavailable","type":"server_error"}}`, http.StatusBadGateway)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
	return nil
}

func (r *Runtime) Transcribe(context.Context, http.ResponseWriter, *http.Request) error {
	return unix.ErrNotImplemented
}

func rewriteModelFromBody(req *http.Request) (model string, body []byte, err error) {
	if req.Body == nil {
		return "", nil, nil
	}
	body, err = io.ReadAll(req.Body)
	if err != nil {
		return "", nil, err
	}
	_ = req.Body.Close()
	var parsed struct {
		Model string `json:"model"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			return "", nil, err
		}
	}
	return strings.TrimSpace(parsed.Model), body, nil
}

func dialJSON(ctx context.Context, socket string, body []byte) ([]byte, error) {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		return nil, &unix.Error{Code: unix.CodeBackendUnavailable, Message: "no socket"}
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	if _, err := conn.Write(body); err != nil {
		return nil, err
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	return io.ReadAll(conn)
}
