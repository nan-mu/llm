package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"encore.app/control"
	"encore.app/internal/modelstate"
	"encore.app/unix/mlxlm"
	"encore.dev/middleware"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
)

const (
	routeChat           = "POST /v1/chat/completions"
	backendRecoverWait  = 3 * time.Minute
	backendRecoverPoll  = 500 * time.Millisecond
)

//encore:service
type Service struct {
	chat *mlxlm.ChatProxy
}

var db = sqldb.NewDatabase("gateway", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

func initService() (*Service, error) {
	proxy, err := mlxlm.NewChatProxy()
	if err != nil {
		return nil, err
	}
	return &Service{chat: proxy}, nil
}

// Health reports that the gateway service is up.
//
//encore:api public method=GET path=/health
func (s *Service) Health(ctx context.Context) error {
	var n int
	return db.QueryRow(ctx, "SELECT 1").Scan(&n)
}

// ChatMessage is one OpenAI chat message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ResponseFormat is OpenAI response_format.
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatCompletionsRequest is the OpenAI chat completions body (typed for Encore UI).
type ChatCompletionsRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ChatChoice is one completion choice.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage is token usage.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIError is the OpenAI error object (nested under "error").
type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// ChatCompletionsResponse is either a completion or an OpenAI error envelope.
// Business failures set Error and return err=nil so the JSON stays {"error":{...}}
// instead of Encore's {"code","message"}; openaiHTTPStatus sets the HTTP status.
type ChatCompletionsResponse struct {
	ID      string       `json:"id,omitempty"`
	Object  string       `json:"object,omitempty"`
	Created int64        `json:"created,omitempty"`
	Model   string       `json:"model,omitempty"`
	Choices []ChatChoice `json:"choices,omitempty"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
	Error   *OpenAIError `json:"error,omitempty"`
}

// ChatCompletions is the OpenAI-compatible chat endpoint. Unauthenticated in this slice.
// Typed request (Encore panel forms) + OpenAI error body via Error field + middleware status.
//
//encore:api public method=POST path=/v1/chat/completions tag:openai
func (s *Service) ChatCompletions(ctx context.Context, req *ChatCompletionsRequest) (*ChatCompletionsResponse, error) {
	if req == nil || strings.TrimSpace(req.Model) == "" {
		return openaiError("invalid_request_error", "model required", ""), nil
	}
	if len(req.Messages) == 0 {
		return openaiError("invalid_request_error", "messages required", ""), nil
	}
	if req.Stream {
		return openaiError("invalid_request_error", "streaming not supported", ""), nil
	}

	catalogID := strings.TrimSpace(req.Model)
	snap, err := control.GetSnapshot(ctx, catalogID)
	if err != nil {
		return openaiError("invalid_request_error", "model not found", "model_not_found"), nil
	}
	if snap.Purpose != string(modelstate.PurposeTranslation) {
		return openaiError("invalid_request_error",
			"model purpose does not match chat completions", "model_purpose_mismatch"), nil
	}

	enabled, err := control.RouteEnabled(ctx, &control.RouteEnabledParams{Route: routeChat})
	if err != nil {
		rlog.Error("route enabled check failed", "event", "gateway.route_check_failed", "err", err)
		return openaiError("server_error", "route check failed", ""), nil
	}
	if !enabled.Enabled {
		// Permanent until an operator loads a model — must not look like a transient 5xx.
		return openaiError("invalid_request_error",
			"chat route disabled: no loaded translation model", "route_disabled"), nil
	}
	if snap.Observed != string(modelstate.ModelLoaded) {
		return openaiError("invalid_request_error", "model not loaded", "model_not_loaded"), nil
	}
	if modelstate.FrontendKind(snap.Frontend) != modelstate.FrontendMlxlm || s.chat == nil {
		return openaiError("invalid_request_error", "frontend does not support chat", "frontend_unsupported"), nil
	}

	fwd := *req
	fwd.Model = snap.NativeID
	body, err := json.Marshal(&fwd)
	if err != nil {
		return openaiError("invalid_request_error", "invalid request body", ""), nil
	}

	status, raw, err := s.chat.PostChatCompletions(ctx, snap.NativeID, body)
	if backendTransient(err, status) {
		rlog.Warn("chat backend unavailable; waiting for worker recovery",
			"event", "gateway.chat_recover_wait",
			"model", catalogID,
			"err", err,
			"status", status,
		)
		status, raw, err = s.waitRecoverChat(ctx, catalogID, snap.NativeID, body)
	}
	if err != nil {
		rlog.Error("chat proxy failed", "event", "gateway.chat_failed", "err", err, "model", catalogID)
		// Still transient from the client's view only if recovery failed mid-flight.
		return openaiError("server_error", "backend unavailable", "backend_unavailable"), nil
	}
	if status >= 400 {
		if out, ok := parseBackendOpenAIError(raw); ok {
			return out, nil
		}
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = "backend error"
		}
		typ := "server_error"
		if status < 500 {
			typ = "invalid_request_error"
		}
		return openaiError(typ, msg, ""), nil
	}

	var out ChatCompletionsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return openaiError("server_error", "invalid backend response", ""), nil
	}
	out.Model = catalogID
	out.Error = nil
	return &out, nil
}

func backendTransient(err error, status int) bool {
	if err != nil {
		return true
	}
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status >= 500
}

// waitRecoverChat polls until control reloads the worker and a chat POST succeeds,
// or until the deadline / context cancel. Absorbs mid-flight worker crashes so
// long BabelDOC requests (timeout 600s) can complete without client-side storms.
func (s *Service) waitRecoverChat(ctx context.Context, catalogID, nativeID string, body []byte) (int, []byte, error) {
	deadline := time.Now().Add(backendRecoverWait)
	var lastStatus int
	var lastRaw []byte
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastStatus, lastRaw, lastErr
			}
			return 0, nil, ctx.Err()
		case <-time.After(backendRecoverPoll):
		}

		snap, err := control.GetSnapshot(ctx, catalogID)
		if err != nil {
			lastErr = err
			continue
		}
		if snap.Desired != string(modelstate.ModelLoaded) {
			return http.StatusBadRequest, []byte(`{"error":{"message":"model not loaded","type":"invalid_request_error","code":"model_not_loaded"}}`), nil
		}
		if snap.Observed != string(modelstate.ModelLoaded) {
			continue
		}
		lastStatus, lastRaw, lastErr = s.chat.PostChatCompletions(ctx, nativeID, body)
		if !backendTransient(lastErr, lastStatus) {
			rlog.Info("chat backend recovered",
				"event", "gateway.chat_recover_ok",
				"model", catalogID,
			)
			return lastStatus, lastRaw, lastErr
		}
	}
	if lastErr != nil {
		return lastStatus, lastRaw, lastErr
	}
	if lastStatus != 0 {
		return lastStatus, lastRaw, lastErr
	}
	return http.StatusBadGateway, nil, nil
}

// openaiHTTPStatus sets 4xx/5xx when the handler returned an OpenAI error payload
// with err=nil (so the body is {"error":...} instead of Encore errs JSON).
// Also sets X-Should-Retry so openai-python does not auto-retry hard failures.
//
//encore:middleware target=tag:openai
func openaiHTTPStatus(req middleware.Request, next middleware.Next) middleware.Response {
	resp := next(req)
	if resp.Err != nil {
		return resp
	}
	out, ok := resp.Payload.(*ChatCompletionsResponse)
	if !ok || out == nil || out.Error == nil {
		return resp
	}
	resp.HTTPStatus = openAIErrorHTTPStatus(out.Error)
	if !openAIErrorRetryable(out.Error) {
		resp.Header().Set("X-Should-Retry", "false")
	}
	return resp
}

func openaiError(typ, message, code string) *ChatCompletionsResponse {
	return &ChatCompletionsResponse{
		Error: &OpenAIError{Message: message, Type: typ, Code: code},
	}
}

func parseBackendOpenAIError(raw []byte) (*ChatCompletionsResponse, bool) {
	var out ChatCompletionsResponse
	if err := json.Unmarshal(raw, &out); err != nil || out.Error == nil {
		return nil, false
	}
	return &out, true
}

func openAIErrorHTTPStatus(e *OpenAIError) int {
	if e == nil {
		return http.StatusBadGateway
	}
	switch e.Code {
	case "model_not_found":
		return http.StatusNotFound
	case "model_purpose_mismatch", "route_disabled", "model_not_loaded", "frontend_unsupported":
		// 4xx: openai-python only auto-retries 408/409/429/5xx. BabelDOC tenacity
		// only retries RateLimitError. Catalog/hard failures must not look transient.
		return http.StatusBadRequest
	case "backend_unavailable":
		return http.StatusBadGateway
	}
	if e.Type == "invalid_request_error" {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

func openAIErrorRetryable(e *OpenAIError) bool {
	if e == nil {
		return false
	}
	// Gateway already waited for worker reload on backend_unavailable; do not
	// invite openai-python / BabelDOC to stampede the endpoint.
	return false
}

// ListModelsResponse is the OpenAI models list shape.
type ListModelsResponse struct {
	Object string       `json:"object"`
	Data   []ModelEntry `json:"data"`
}

// ModelEntry is one OpenAI model card.
type ModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ListModels lists loaded translation models.
//
//encore:api public method=GET path=/v1/models
func (s *Service) ListModels(ctx context.Context) (*ListModelsResponse, error) {
	all, err := control.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	out := &ListModelsResponse{Object: "list", Data: []ModelEntry{}}
	for _, m := range all.Models {
		if m.Purpose != string(modelstate.PurposeTranslation) {
			continue
		}
		if m.Observed != string(modelstate.ModelLoaded) {
			continue
		}
		out.Data = append(out.Data, ModelEntry{
			ID:      m.ID,
			Object:  "model",
			OwnedBy: "local",
		})
	}
	return out, nil
}
