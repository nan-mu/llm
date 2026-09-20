package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"encore.app/control"
	"encore.app/frontend/llama"
	"encore.app/gateway/validate"
	"encore.app/internal/modelstate"
	unixsvc "encore.app/unix"
	"encore.dev/middleware"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
)

const (
	routeChat          = validate.RouteChat
	routeTranslations  = validate.RouteTranslations
	backendRecoverWait = 3 * time.Minute
	backendRecoverPoll = 500 * time.Millisecond
)

//encore:service
type Service struct {
	llamaChat *llama.ChatProxy
}

var db = sqldb.NewDatabase("gateway", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

func initService() (*Service, error) {
	llamaProxy, err := llama.NewChatProxy()
	if err != nil {
		return nil, err
	}
	return &Service{llamaChat: llamaProxy}, nil
}

// Health reports that the gateway service is up.
//
//encore:api public method=GET path=/health
func (s *Service) Health(ctx context.Context) error {
	var n int
	return db.QueryRow(ctx, "SELECT 1").Scan(&n)
}

// ChatMessage is one OpenAI chat message (string content only).
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionsRequest is the OpenAI chat completions body (strict OpenAI fields).
type ChatCompletionsRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

// chatForwardBody is marshaled to the Unix worker (may include injected top_k / repetition_penalty).
type chatForwardBody struct {
	Model             string        `json:"model"`
	Messages          []ChatMessage `json:"messages"`
	Temperature       *float64      `json:"temperature,omitempty"`
	TopP              *float64      `json:"top_p,omitempty"`
	TopK              *int          `json:"top_k,omitempty"`
	RepetitionPenalty *float64      `json:"repetition_penalty,omitempty"`
	MaxTokens         *int          `json:"max_tokens,omitempty"`
	Stream            bool          `json:"stream,omitempty"`
}

// ChatChoice is one completion choice.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage is OpenAI token usage.
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
type ChatCompletionsResponse struct {
	ID      string       `json:"id,omitempty"`
	Object  string       `json:"object,omitempty"`
	Created int64        `json:"created,omitempty"`
	Model   string       `json:"model,omitempty"`
	Choices []ChatChoice `json:"choices,omitempty"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
	Error   *OpenAIError `json:"error,omitempty"`
}

// ChatCompletions proxies a validated chat body to the matching Unix ChatProxy.
//
//encore:api public method=POST path=/v1/chat/completions tag:openai
func (s *Service) ChatCompletions(ctx context.Context, req *ChatCompletionsRequest) (*ChatCompletionsResponse, error) {
	if req == nil || strings.TrimSpace(req.Model) == "" {
		return openaiError("invalid_request_error", "model required", ""), nil
	}

	catalogID := strings.TrimSpace(req.Model)
	snap, err := control.GetSnapshot(ctx, catalogID)
	if err != nil {
		return openaiError("invalid_request_error", "model not found", "model_not_found"), nil
	}
	purpose := modelstate.Purpose(snap.Purpose)
	if purpose != modelstate.PurposeTranslation {
		return openaiError("invalid_request_error",
			"model purpose does not match chat completions", "model_purpose_mismatch"), nil
	}
	if snap.Observed != string(modelstate.ModelLoaded) {
		return openaiError("invalid_request_error", "model not loaded", "model_not_loaded"), nil
	}
	frontend := modelstate.FrontendKind(snap.Frontend)
	if frontend != modelstate.FrontendLlama && frontend != modelstate.FrontendMlxlm {
		return openaiError("invalid_request_error", "frontend does not support chat", "frontend_unsupported"), nil
	}

	fwd := chatForwardBody{
		Model:       snap.NativeID,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}
	if rw := rewrittenChatFromCtx(ctx); rw != nil {
		fwd.Temperature = rw.Temperature
		fwd.TopP = rw.TopP
		fwd.TopK = rw.TopK
		fwd.RepetitionPenalty = rw.RepetitionPenalty
		fwd.MaxTokens = rw.MaxTokens
		fwd.Stream = rw.Stream
		fwd.Messages = make([]ChatMessage, len(rw.Messages))
		for i, m := range rw.Messages {
			var content string
			_ = json.Unmarshal(m.Content, &content)
			fwd.Messages[i] = ChatMessage{Role: m.Role, Content: content}
		}
	}

	body, err := json.Marshal(&fwd)
	if err != nil {
		return openaiError("invalid_request_error", "invalid request body", ""), nil
	}

	status, raw, err := s.postChat(ctx, frontend, snap.NativeID, body)
	if backendTransient(err, status) {
		rlog.Warn("chat backend unavailable; waiting for worker recovery",
			"event", "gateway.chat_recover_wait",
			"model", catalogID,
			"err", err,
			"status", status,
		)
		status, raw, err = s.waitRecoverChat(ctx, catalogID, snap.NativeID, frontend, body)
	}
	if err != nil {
		if _, ok := err.(frontendUnsupportedError); ok {
			return openaiError("invalid_request_error", "frontend does not support chat", "frontend_unsupported"), nil
		}
		rlog.Error("chat proxy failed", "event", "gateway.chat_failed", "err", err, "model", catalogID)
		return openaiError("server_error", "backend unavailable", "backend_unavailable"), nil
	}
	if status >= 400 {
		if out, ok := parseBackendOpenAIError(raw); ok {
			return out, nil
		}
		if out, ok := parseWorkerInferError(raw); ok {
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

	if frontend == modelstate.FrontendMlxlm {
		text, usage, err := parseWorkerInfer(raw)
		if err != nil {
			return openaiError("server_error", "invalid backend response", ""), nil
		}
		return &ChatCompletionsResponse{
			Object:  "chat.completion",
			Model:   catalogID,
			Choices: []ChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: text}, FinishReason: "stop"}},
			Usage:   usage,
		}, nil
	}

	var out ChatCompletionsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return openaiError("server_error", "invalid backend response", ""), nil
	}
	out.Model = catalogID
	out.Error = nil
	return &out, nil
}

func (s *Service) postChat(ctx context.Context, frontend modelstate.FrontendKind, nativeID string, body []byte) (int, []byte, error) {
	switch frontend {
	case modelstate.FrontendLlama:
		if s.llamaChat == nil {
			return http.StatusBadRequest, nil, errFrontendUnsupported
		}
		return s.llamaChat.PostChatCompletions(ctx, nativeID, body, nil)
	case modelstate.FrontendMlxlm:
		res, err := unixsvc.Chat(ctx, nativeID, &unixsvc.ChatBody{Body: body})
		if err != nil {
			return http.StatusBadGateway, nil, err
		}
		return http.StatusOK, []byte(res.Body), nil
	default:
		return http.StatusBadRequest, nil, errFrontendUnsupported
	}
}

type frontendUnsupportedError struct{}

func (frontendUnsupportedError) Error() string { return "frontend_unsupported" }

var errFrontendUnsupported = frontendUnsupportedError{}

func backendTransient(err error, status int) bool {
	if err != nil {
		if _, ok := err.(frontendUnsupportedError); ok {
			return false
		}
		return true
	}
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status >= 500
}

func (s *Service) waitRecoverChat(ctx context.Context, catalogID, nativeID string, frontend modelstate.FrontendKind, body []byte) (int, []byte, error) {
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
		lastStatus, lastRaw, lastErr = s.postChat(ctx, frontend, nativeID, body)
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

type workerInferResponse struct {
	Content string     `json:"content"`
	Usage   *ChatUsage `json:"usage"`
	Error   *OpenAIError `json:"error"`
}

func parseWorkerInfer(raw []byte) (string, *ChatUsage, error) {
	var out workerInferResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", nil, err
	}
	if out.Error != nil {
		return "", out.Usage, fmt.Errorf("%s", out.Error.Message)
	}
	return out.Content, out.Usage, nil
}

func parseWorkerInferError(raw []byte) (*ChatCompletionsResponse, bool) {
	var out workerInferResponse
	if err := json.Unmarshal(raw, &out); err != nil || out.Error == nil {
		return nil, false
	}
	return openaiError(out.Error.Type, out.Error.Message, out.Error.Code), true
}

func openAIErrorHTTPStatus(e *OpenAIError) int {
	if e == nil {
		return http.StatusBadGateway
	}
	switch e.Code {
	case "model_not_found":
		return http.StatusNotFound
	case "model_purpose_mismatch", "route_disabled", "model_not_loaded", "frontend_unsupported":
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

// ListModels lists loaded translation models (BabelDOC / HY-MT2). Not Gemma.
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
