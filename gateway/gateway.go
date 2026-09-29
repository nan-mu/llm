package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"errors"

	"encore.app/control"
	llamaproxy "encore.app/frontend/llama/proxy"
	"encore.app/internal/modelstate"
	unixsvc "encore.app/unix"
	"encore.dev/rlog"
)

var (
	errBackend  = errors.New("backend unavailable")
	errFrontend = errors.New("frontend unsupported")
)

const httpOK = 200

const (
	backendRecoverWait = 3 * time.Minute
	backendRecoverPoll = 500 * time.Millisecond
)

// HealthResponse is the gateway health body.
type HealthResponse struct {
	OK bool `json:"ok"`
}

// Health reports that the gateway is up. This slice has no auth.
//
//encore:api public method=GET path=/health
func Health(ctx context.Context) (*HealthResponse, error) {
	return &HealthResponse{OK: true}, nil
}

// ChatMessage is one OpenAI chat message. Content is a string.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionsRequest is the OpenAI chat completions body for purpose=translation.
type ChatCompletionsRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
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

// OpenAIError is the OpenAI error object nested under "error".
type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// ChatCompletionsResponse is a completion or an OpenAI error envelope.
type ChatCompletionsResponse struct {
	ID      string       `json:"id,omitempty"`
	Object  string       `json:"object,omitempty"`
	Created int64        `json:"created,omitempty"`
	Model   string       `json:"model,omitempty"`
	Choices []ChatChoice `json:"choices,omitempty"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
	Error   *OpenAIError `json:"error,omitempty"`
}

// ChatCompletions serves purpose=translation only. structured_translation uses POST /v1/translations.
//
//encore:api public method=POST path=/v1/chat/completions tag:openai
func ChatCompletions(ctx context.Context, req *ChatCompletionsRequest) (*ChatCompletionsResponse, error) {
	if req == nil || strings.TrimSpace(req.Model) == "" {
		return openaiError("invalid_request_error", "model required", ""), nil
	}
	catalogID := strings.TrimSpace(req.Model)
	snap, err := control.GetSnapshot(ctx, catalogID)
	if err != nil {
		return openaiError("invalid_request_error", "model not found", "model_not_found"), nil
	}
	if modelstate.Purpose(snap.Purpose) != modelstate.PurposeTranslation {
		return openaiError("invalid_request_error", "model purpose does not match chat completions", "model_purpose_mismatch"), nil
	}
	if snap.Observed != string(modelstate.ModelLoaded) {
		return openaiError("invalid_request_error", "model not loaded", "model_not_loaded"), nil
	}
	frontend := modelstate.FrontendKind(snap.Frontend)
	if frontend != modelstate.FrontendLlama && frontend != modelstate.FrontendMlxlm {
		return openaiError("invalid_request_error", "frontend does not support chat", "frontend_unsupported"), nil
	}

	body, err := chatForward(ctx, req, snap.NativeID)
	if err != nil {
		return openaiError("invalid_request_error", "invalid request body", ""), nil
	}
	status, raw, err := postChat(ctx, frontend, snap, body)
	if errors.Is(err, errFrontend) {
		return openaiError("invalid_request_error", "frontend does not support chat", "frontend_unsupported"), nil
	}
	if backendTransient(err, status) {
		status, raw, err = waitRecoverChat(ctx, catalogID, frontend, body)
	}
	if err != nil {
		rlog.Error("chat backend unavailable", "model", catalogID, "err", err)
		return openaiError("server_error", "backend unavailable", "backend_unavailable"), nil
	}
	if status >= 400 {
		if resp, ok := parseWorkerChatError(raw); ok {
			return resp, nil
		}
		return openaiError("server_error", "backend unavailable", "backend_unavailable"), nil
	}
	text, usage := parseWorkerChat(raw)
	return &ChatCompletionsResponse{
		ID:      newID("chatcmpl-"),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   catalogID,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
		Usage: usage,
	}, nil
}

func chatForward(ctx context.Context, req *ChatCompletionsRequest, nativeID string) ([]byte, error) {
	fwd := map[string]any{
		"model":    nativeID,
		"messages": req.Messages,
		"stream":   false,
	}
	if req.Temperature != nil {
		fwd["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		fwd["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		fwd["max_tokens"] = *req.MaxTokens
	}
	if rw := rewrittenChatFromCtx(ctx); rw != nil {
		msgs := make([]ChatMessage, len(rw.Messages))
		for i, m := range rw.Messages {
			var content string
			_ = json.Unmarshal(m.Content, &content)
			msgs[i] = ChatMessage{Role: m.Role, Content: content}
		}
		fwd["messages"] = msgs
		if rw.Temperature != nil {
			fwd["temperature"] = *rw.Temperature
		}
		if rw.TopP != nil {
			fwd["top_p"] = *rw.TopP
		}
		if rw.MaxTokens != nil {
			fwd["max_tokens"] = *rw.MaxTokens
		}
		if rw.TopK != nil {
			fwd["top_k"] = *rw.TopK
		}
		if rw.RepetitionPenalty != nil {
			fwd["repetition_penalty"] = *rw.RepetitionPenalty
		}
		fwd["stream"] = false
	}
	return json.Marshal(fwd)
}

func postChat(ctx context.Context, kind modelstate.FrontendKind, snap *control.Snapshot, body []byte) (int, []byte, error) {
	switch kind {
	case modelstate.FrontendMlxlm:
		res, err := unixsvc.Chat(ctx, snap.NativeID, &unixsvc.ChatBody{Body: body})
		if err != nil {
			return 0, nil, err
		}
		if res == nil {
			return 0, nil, errBackend
		}
		return httpOK, res.Body, nil
	case modelstate.FrontendLlama:
		sock := ""
		if snap.SocketPath != nil {
			sock = *snap.SocketPath
		}
		return llamaproxy.Post(ctx, sock, snap.NativeID, body)
	default:
		return 0, nil, errFrontend
	}
}

func waitRecoverChat(ctx context.Context, catalogID string, kind modelstate.FrontendKind, body []byte) (int, []byte, error) {
	rlog.Warn("chat backend unavailable; waiting for worker recovery", "model", catalogID)
	deadline := time.Now().Add(backendRecoverWait)
	var status int
	var raw []byte
	var err error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-time.After(backendRecoverPoll):
		}
		next, snapErr := control.GetSnapshot(ctx, catalogID)
		if snapErr != nil || next.Observed != string(modelstate.ModelLoaded) {
			continue
		}
		status, raw, err = postChat(ctx, kind, next, body)
		if !backendTransient(err, status) {
			return status, raw, err
		}
	}
	return status, raw, err
}

func backendTransient(err error, status int) bool {
	if err != nil {
		return true
	}
	return status >= 500
}

func parseWorkerChat(raw []byte) (string, *ChatUsage) {
	var out struct {
		Content string `json:"content"`
		Choices []struct {
			Message ChatMessage `json:"message"`
		} `json:"choices"`
		Usage *ChatUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw), nil
	}
	if out.Content != "" {
		return out.Content, out.Usage
	}
	if len(out.Choices) > 0 {
		return out.Choices[0].Message.Content, out.Usage
	}
	return "", out.Usage
}

func parseWorkerChatError(raw []byte) (*ChatCompletionsResponse, bool) {
	var out struct {
		Error *OpenAIError `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Error == nil || out.Error.Message == "" {
		return nil, false
	}
	return &ChatCompletionsResponse{Error: out.Error}, true
}

func openaiError(typ, message, code string) *ChatCompletionsResponse {
	return &ChatCompletionsResponse{Error: &OpenAIError{Type: typ, Message: message, Code: code}}
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// ListModelsResponse is the OpenAI models list.
type ListModelsResponse struct {
	Object string       `json:"object"`
	Data   []ModelEntry `json:"data"`
}

// ModelEntry is one model card.
type ModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ListModels lists loaded translation and structured_translation models. ASR is omitted.
//
//encore:api public method=GET path=/v1/models
func ListModels(ctx context.Context) (*ListModelsResponse, error) {
	all, err := control.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	out := &ListModelsResponse{Object: "list", Data: []ModelEntry{}}
	for _, m := range all.Models {
		purpose := modelstate.Purpose(m.Purpose)
		if purpose != modelstate.PurposeTranslation && purpose != modelstate.PurposeStructuredTranslation {
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
