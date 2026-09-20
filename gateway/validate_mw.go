package gateway

import (
	"context"
	"encoding/json"

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
	_, err := validate.PurposeContract(req.Context(), vreq)
	if err != nil {
		return rejectOpenAI(err)
	}
	fromValidateRequest(payload, vreq)
	ctx := context.WithValue(req.Context(), rewrittenChatCtxKey{}, vreq)
	return next(req.WithContext(ctx))
}

func rejectOpenAI(e *validate.Error) middleware.Response {
	return middleware.Response{
		Payload: openaiError(e.Type, e.Message, e.Code),
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

func fromValidateRequest(p *ChatCompletionsRequest, v *validate.Request) {
	p.Model = v.Model
	p.Temperature = v.Temperature
	p.TopP = v.TopP
	p.MaxTokens = v.MaxTokens
	p.Stream = v.Stream
	p.Messages = make([]ChatMessage, len(v.Messages))
	for i, m := range v.Messages {
		var content string
		_ = json.Unmarshal(m.Content, &content)
		p.Messages[i] = ChatMessage{Role: m.Role, Content: content}
	}
}
