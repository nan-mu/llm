package mlxlm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"encore.app/unix"
	"encore.app/unix/internal/httpx"
)

// ChatProxy is a dial-only Inferencer. It does not Start/Load processes.
// Socket path convention matches Runtime.Load: {Cwd}/{nativeID}.sock
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
	_ = ctx
	nativeID, body, err := rewriteModelFromBody(req)
	if err != nil {
		http.Error(w, `{"error":{"message":"invalid request body","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return nil
	}
	if nativeID == "" {
		http.Error(w, `{"error":{"message":"model required","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return nil
	}
	req2 := req.Clone(req.Context())
	req2.Body = io.NopCloser(bytes.NewReader(body))
	req2.ContentLength = int64(len(body))
	req2.URL.Path = "/v1/chat/completions"
	req2.RequestURI = ""
	return httpx.ReverseProxyUnix(p.SocketPath(nativeID), p.APIKey, w, req2)
}

// PostChatCompletions dials the model sock and POSTs a JSON chat body.
// model in body should already be the native id.
func (p *ChatProxy) PostChatCompletions(ctx context.Context, nativeID string, body []byte) (status int, resp []byte, err error) {
	nativeID = strings.TrimSpace(nativeID)
	if nativeID == "" {
		return http.StatusBadRequest, nil, &unix.Error{Code: unix.CodeNotFound, Message: "model required"}
	}
	return httpx.DoUnixJSON(ctx, p.SocketPath(nativeID), p.APIKey, http.MethodPost, "/v1/chat/completions", body)
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
		// Fall back to path convention when supervisor entry is missing but sock exists.
		sock = filepath.Join(r.cfg.Cwd, nativeID+".sock")
		if _, err := os.Stat(sock); err != nil {
			http.Error(w, `{"error":{"message":"model not loaded","type":"server_error","code":"model_not_loaded"}}`, http.StatusServiceUnavailable)
			return nil
		}
	}
	req2 := req.Clone(ctx)
	req2.Body = io.NopCloser(bytes.NewReader(body))
	req2.ContentLength = int64(len(body))
	req2.URL.Path = "/v1/chat/completions"
	req2.RequestURI = ""
	return httpx.ReverseProxyUnix(sock, r.cfg.APIKey, w, req2)
}

func (r *Runtime) Transcribe(context.Context, http.ResponseWriter, *http.Request) error {
	return unix.ErrNotImplemented
}

// rewriteModelFromBody keeps the body but returns the model field (caller may have
// already rewritten catalog id → native_id). Does not change the body itself.
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
