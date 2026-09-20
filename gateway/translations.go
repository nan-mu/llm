package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"encore.app/control"
	"encore.app/gateway/validate"
	"encore.app/internal/modelstate"
	unixsvc "encore.app/unix"
	"encore.dev/middleware"
	"encore.dev/rlog"
)

// TranslationsRequest is the document-oriented structured translation batch body.
type TranslationsRequest struct {
	Model          string               `json:"model"`
	SourceLanguage string               `json:"source_language"`
	TargetLanguage string               `json:"target_language"`
	Context        *TranslationContext  `json:"context,omitempty"`
	Glossaries     []Glossary           `json:"glossaries,omitempty"`
	Inputs         []TranslationInput   `json:"inputs"`
}

// TranslationContext is optional document context for TranslateGemma.
type TranslationContext struct {
	DocumentTitle string `json:"document_title,omitempty"`
	RecentTitle   string `json:"recent_title,omitempty"`
}

// Glossary is a named term list.
type Glossary struct {
	Name    string          `json:"name"`
	Entries []GlossaryEntry `json:"entries"`
}

// GlossaryEntry is one source→target term.
type GlossaryEntry struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// TranslationInput is one segment to translate.
type TranslationInput struct {
	ID               int               `json:"id"`
	Text             string            `json:"text"`
	LayoutLabel      string            `json:"layout_label,omitempty"`
	PlaceholderHints map[string]string `json:"placeholder_hints,omitempty"`
}

// TranslationsResponse is object=translation.batch.
type TranslationsResponse struct {
	Object       string              `json:"object,omitempty"`
	Model        string              `json:"model,omitempty"`
	Translations []TranslationItem   `json:"translations,omitempty"`
	Usage        *TranslationUsage   `json:"usage,omitempty"`
	Error        *OpenAIError        `json:"error,omitempty"`
}

// TranslationItem is one id-aligned output.
type TranslationItem struct {
	ID     int    `json:"id"`
	Output string `json:"output"`
}

// TranslationUsage uses input/output_tokens (not OpenAI prompt/completion names).
type TranslationUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Translations runs structured_translation (TranslateGemma) over a batch of inputs.
//
//encore:api public method=POST path=/v1/translations tag:translations
func (s *Service) Translations(ctx context.Context, req *TranslationsRequest) (*TranslationsResponse, error) {
	if errResp := validateTranslationsRequest(ctx, req); errResp != nil {
		return errResp, nil
	}

	catalogID := strings.TrimSpace(req.Model)
	snap, err := control.GetSnapshot(ctx, catalogID)
	if err != nil {
		return translationsError("invalid_request_error", "model not found", "model_not_found"), nil
	}
	if snap.Purpose != string(modelstate.PurposeStructuredTranslation) {
		return translationsError("invalid_request_error",
			"model purpose does not match translations", "model_purpose_mismatch"), nil
	}
	enabled, err := control.RouteEnabled(ctx, &control.RouteEnabledParams{
		Route:   routeTranslations,
		Purpose: string(modelstate.PurposeStructuredTranslation),
	})
	if err != nil {
		rlog.Error("translations route check failed", "event", "gateway.translations_route_check_failed", "err", err)
		return translationsError("server_error", "route check failed", ""), nil
	}
	if !enabled.Enabled {
		return translationsError("invalid_request_error",
			"translations route disabled: no loaded structured_translation model", "route_disabled"), nil
	}
	if snap.Observed != string(modelstate.ModelLoaded) {
		return translationsError("invalid_request_error", "model not loaded", "model_not_loaded"), nil
	}
	if modelstate.FrontendKind(snap.Frontend) != modelstate.FrontendMlxlm {
		return translationsError("invalid_request_error", "frontend does not support translations", "frontend_unsupported"), nil
	}

	contract, err := control.GetPurposeStructuredTranslation(ctx)
	if err != nil {
		return translationsError("server_error", "purpose contract unavailable", ""), nil
	}
	templateID := ""
	if contract.RequireChatTemplate {
		templateID = contract.ChatTemplateID
	}

	maxTokens := 4096
	if contract.MaxTokensDefault != nil {
		maxTokens = *contract.MaxTokensDefault
	}
	var temperature *float64
	if contract.TemperatureDefault != nil {
		temperature = contract.TemperatureDefault
	}

	out := &TranslationsResponse{
		Object:       "translation.batch",
		Model:        catalogID,
		Translations: make([]TranslationItem, 0, len(req.Inputs)),
		Usage:        &TranslationUsage{},
	}

	for _, in := range req.Inputs {
		body, err := buildTranslateGemmaChatBody(req, in, temperature, maxTokens, templateID)
		if err != nil {
			return translationsError("invalid_request_error", err.Error(), ""), nil
		}
		res, err := unixsvc.Chat(ctx, snap.NativeID, &unixsvc.ChatBody{Body: body})
		if err != nil {
			rlog.Error("translations proxy failed",
				"event", "gateway.translations_failed",
				"model", catalogID,
				"input_id", in.ID,
				"err", err,
			)
			return translationsError("server_error", "backend unavailable", "backend_unavailable"), nil
		}
		text, usage, err := parseWorkerInfer([]byte(res.Body))
		if err != nil {
			if ce, ok := parseWorkerInferError([]byte(res.Body)); ok && ce.Error != nil {
				return translationsError(ce.Error.Type, ce.Error.Message, ce.Error.Code), nil
			}
			return translationsError("server_error", "invalid backend response", ""), nil
		}
		out.Translations = append(out.Translations, TranslationItem{
			ID:     in.ID,
			Output: extractTranslationOutput(text),
		})
		if usage != nil {
			out.Usage.InputTokens += usage.PromptTokens
			out.Usage.OutputTokens += usage.CompletionTokens
			out.Usage.TotalTokens += usage.TotalTokens
		}
	}
	return out, nil
}

func validateTranslationsRequest(ctx context.Context, req *TranslationsRequest) *TranslationsResponse {
	if req == nil || strings.TrimSpace(req.Model) == "" {
		return translationsError("invalid_request_error", "model required", "")
	}
	if len(req.Inputs) == 0 {
		return translationsError("invalid_request_error", "inputs required", "")
	}
	for _, in := range req.Inputs {
		if strings.TrimSpace(in.Text) == "" {
			return translationsError("invalid_request_error", "input text required", "")
		}
	}
	if e := validate.LangOK(ctx, req.SourceLanguage); e != nil {
		return translationsError(e.Type, e.Message, e.Code)
	}
	if e := validate.LangOK(ctx, req.TargetLanguage); e != nil {
		return translationsError(e.Type, e.Message, e.Code)
	}
	return nil
}

func buildTranslateGemmaChatBody(req *TranslationsRequest, in TranslationInput, temperature *float64, maxTokens int, templateID string) ([]byte, error) {
	// Prompt-only JSON for unix → mlxlm (no catalog/purpose/route fields).
	part := map[string]any{
		"type":             "text",
		"source_lang_code": strings.TrimSpace(req.SourceLanguage),
		"target_lang_code": strings.TrimSpace(req.TargetLanguage),
		"text":             in.Text,
	}
	if req.Context != nil {
		if t := strings.TrimSpace(req.Context.DocumentTitle); t != "" {
			part["document_title"] = t
		}
		if t := strings.TrimSpace(req.Context.RecentTitle); t != "" {
			part["recent_title"] = t
		}
	}
	if t := strings.TrimSpace(in.LayoutLabel); t != "" {
		part["layout_label"] = t
	}
	if gloss := flattenGlossary(req.Glossaries); len(gloss) > 0 {
		part["glossary"] = gloss
	}
	if len(in.PlaceholderHints) > 0 {
		part["placeholder_hints"] = in.PlaceholderHints
	}
	msg := map[string]any{
		"role":    "user",
		"content": []any{part},
	}
	body := map[string]any{
		"messages":   []any{msg},
		"max_tokens": maxTokens,
		"stream":     false,
	}
	if templateID != "" {
		body["chat_template_id"] = templateID
	}
	if temperature != nil {
		body["temperature"] = *temperature
	}
	return json.Marshal(body)
}

func flattenGlossary(glossaries []Glossary) []map[string]string {
	var out []map[string]string
	for _, g := range glossaries {
		for _, e := range g.Entries {
			src := strings.TrimSpace(e.Source)
			if src == "" {
				continue
			}
			out = append(out, map[string]string{
				"source": src,
				"target": strings.TrimSpace(e.Target),
			})
		}
	}
	return out
}

var fenceRe = regexp.MustCompile("(?s)^\\s*```(?:json)?\\s*(.*?)\\s*```\\s*$")

// extractTranslationOutput strips markdown fences and pulls text from typed-part JSON when present.
func extractTranslationOutput(s string) string {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); len(m) == 2 {
		s = strings.TrimSpace(m[1])
	}
	trim := bytes.TrimSpace([]byte(s))
	if len(trim) == 0 {
		return ""
	}
	if trim[0] == '[' {
		var parts []map[string]any
		if err := json.Unmarshal(trim, &parts); err == nil && len(parts) > 0 {
			if t, ok := parts[0]["text"].(string); ok {
				return t
			}
		}
	}
	if trim[0] == '{' {
		var obj map[string]any
		if err := json.Unmarshal(trim, &obj); err == nil {
			if t, ok := obj["text"].(string); ok {
				return t
			}
			if t, ok := obj["translation"].(string); ok {
				return t
			}
			if t, ok := obj["output"].(string); ok {
				return t
			}
		}
	}
	return s
}

func translationsError(typ, message, code string) *TranslationsResponse {
	return &TranslationsResponse{
		Error: &OpenAIError{Message: message, Type: typ, Code: code},
	}
}

//encore:middleware target=tag:translations
func translationsHTTPStatus(req middleware.Request, next middleware.Next) middleware.Response {
	resp := next(req)
	if resp.Err != nil {
		return resp
	}
	out, ok := resp.Payload.(*TranslationsResponse)
	if !ok || out == nil || out.Error == nil {
		return resp
	}
	resp.HTTPStatus = openAIErrorHTTPStatus(out.Error)
	if !openAIErrorRetryable(out.Error) {
		resp.Header().Set("X-Should-Retry", "false")
	}
	return resp
}
