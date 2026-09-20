package llama

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"encore.app/frontend"
	"encore.app/frontend/internal/httpx"
)

// ChatProxy is a dial-only Inferencer. It does not Start/Load processes.
// Socket path convention matches Runtime.Load: {Cwd}/{nativeID}.sock
type ChatProxy struct {
	Cwd    string
	APIKey string
}

var _ frontend.Inferencer = (*ChatProxy)(nil)

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
	_ = w
	_ = req
	return frontend.ErrNotImplemented
}

// PostChatCompletions dials the model sock and POSTs a JSON chat body.
// model in body should already be the native id.
func (p *ChatProxy) PostChatCompletions(ctx context.Context, nativeID string, body []byte, extraHdr http.Header) (status int, resp []byte, err error) {
	nativeID = strings.TrimSpace(nativeID)
	if nativeID == "" {
		return http.StatusBadRequest, nil, &frontend.Error{Code: frontend.CodeNotFound, Message: "model required"}
	}
	return httpx.DoUnixJSONHeaders(ctx, p.SocketPath(nativeID), p.APIKey, http.MethodPost, "/v1/chat/completions", body, extraHdr)
}

func (p *ChatProxy) Transcribe(context.Context, http.ResponseWriter, *http.Request) error {
	return frontend.ErrNotImplemented
}
