package gateway

import (
	"context"
	"encoding/json"
	"net/http"

	"encore.app/gateway/validate"
	"encore.dev/middleware"
)

type rewrittenChatCtxKey struct{}

//encore:middleware target=tag:openai
func openaiValidate(req middleware.Request, next middleware.Next) middleware.Response {
	auth := ""
	if h := req.Data().Headers; h != nil {
		auth = h.Get("Authorization")
	}
	if e := validate.APIToken(auth); e != nil {
		return rejectOpenAI(e)
	}

	payload, ok := req.Data().Payload.(*ChatCompletionsRequest)
	if !ok || payload == nil {
		return next(req)
	}

	vreq := toValidateRequest(payload)
	if _, err := validate.PurposeContract(req.Context(), vreq); err != nil {
		return rejectOpenAI(err)
	}
	ctx := context.WithValue(req.Context(), rewrittenChatCtxKey{}, vreq)
	resp := next(req.WithContext(ctx))
	return withChatStatus(resp)
}

func rejectOpenAI(e *validate.Error) middleware.Response {
	resp := middleware.Response{Payload: openaiError(e.Type, e.Message, e.Code)}
	applyOpenAIStatus(&resp, resp.Payload.(*ChatCompletionsResponse).Error)
	return resp
}

func withChatStatus(resp middleware.Response) middleware.Response {
	if resp.Err != nil || resp.Payload == nil {
		return resp
	}
	out, ok := resp.Payload.(*ChatCompletionsResponse)
	if !ok || out == nil || out.Error == nil {
		return resp
	}
	applyOpenAIStatus(&resp, out.Error)
	return resp
}

//encore:middleware target=tag:translations
func translationsHTTPStatus(req middleware.Request, next middleware.Next) middleware.Response {
	resp := next(req)
	if resp.Err != nil || resp.Payload == nil {
		return resp
	}
	out, ok := resp.Payload.(*TranslationsResponse)
	if !ok || out == nil || out.Error == nil {
		return resp
	}
	applyOpenAIStatus(&resp, out.Error)
	return resp
}

func applyOpenAIStatus(resp *middleware.Response, e *OpenAIError) {
	if e == nil {
		return
	}
	resp.HTTPStatus = openAIErrorHTTPStatus(e)
	if resp.HTTPStatus < 500 {
		resp.Header().Set("X-Should-Retry", "false")
	}
}

func rewrittenChatFromCtx(ctx context.Context) *validate.Request {
	v, _ := ctx.Value(rewrittenChatCtxKey{}).(*validate.Request)
	return v
}

func toValidateRequest(p *ChatCompletionsRequest) *validate.Request {
	msgs := make([]validate.Message, len(p.Messages))
	for i, m := range p.Messages {
		raw, _ := json.Marshal(m.Content)
		msgs[i] = validate.Message{Role: m.Role, Content: raw}
	}
	return &validate.Request{
		Model:       p.Model,
		Messages:    msgs,
		Temperature: p.Temperature,
		TopP:        p.TopP,
		MaxTokens:   p.MaxTokens,
		Stream:      p.Stream,
	}
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
	switch e.Type {
	case "invalid_request_error":
		return http.StatusBadRequest
	case "server_error":
		return http.StatusInternalServerError
	default:
		return http.StatusBadGateway
	}
}
