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
	"encore.dev/rlog"
)

// TranslationsRequest is the document-oriented structured translation batch.
type TranslationsRequest struct {
	Model          string              `json:"model"`
	SourceLanguage string              `json:"source_language"`
	TargetLanguage string              `json:"target_language"`
	Context        *TranslationContext `json:"context,omitempty"`
	Glossaries     []Glossary          `json:"glossaries,omitempty"`
	Inputs         []TranslationInput  `json:"inputs"`
}

// TranslationContext is optional document context. These fields are prompt additives; source text stays in inputs[].text.
type TranslationContext struct {
	DocumentTitle string `json:"document_title,omitempty"`
	RecentTitle   string `json:"recent_title,omitempty"`
}

// Glossary is a named term list.
type Glossary struct {
	Name    string          `json:"name"`
	Entries []GlossaryEntry `json:"entries"`
}

// GlossaryEntry is one source to target term.
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

// TranslationsResponse is object=translation.batch, or an OpenAI error envelope.
type TranslationsResponse struct {
	Object       string            `json:"object,omitempty"`
	Model        string            `json:"model,omitempty"`
	Translations []TranslationItem `json:"translations,omitempty"`
	Usage        *TranslationUsage `json:"usage,omitempty"`
	Error        *OpenAIError      `json:"error,omitempty"`
}

// TranslationItem is one id-aligned output.
type TranslationItem struct {
	ID     int    `json:"id"`
	Output string `json:"output"`
}

// TranslationUsage uses input_tokens and output_tokens.
type TranslationUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Translations is the only structured_translation path. Sampling defaults come from purpose_structured_translation.
// The handler builds prompt-only JSON and calls unix.Chat. It does not accept client sampling fields.
//
//encore:api public method=POST path=/v1/translations tag:translations
func Translations(ctx context.Context, req *TranslationsRequest) (*TranslationsResponse, error) {
	if req == nil || strings.TrimSpace(req.Model) == "" {
		return translationsError("invalid_request_error", "model required", ""), nil
	}
	if len(req.Inputs) == 0 {
		return translationsError("invalid_request_error", "inputs required", ""), nil
	}
	for _, in := range req.Inputs {
		if strings.TrimSpace(in.Text) == "" {
			return translationsError("invalid_request_error", "input text required", ""), nil
		}
	}

	catalogID := strings.TrimSpace(req.Model)
	snap, err := control.GetSnapshot(ctx, catalogID)
	if err != nil {
		return translationsError("invalid_request_error", "model not found", "model_not_found"), nil
	}
	if snap.Purpose != string(modelstate.PurposeStructuredTranslation) {
		return translationsError("invalid_request_error", "model purpose does not match translations", "model_purpose_mismatch"), nil
	}
	enabled, err := control.RouteEnabled(ctx, &control.RouteEnabledParams{
		Route:   validate.RouteTranslations,
		Purpose: string(modelstate.PurposeStructuredTranslation),
	})
	if err != nil {
		rlog.Error("translations route check failed", "err", err)
		return translationsError("server_error", "route check failed", ""), nil
	}
	if !enabled.Enabled {
		return translationsError("invalid_request_error", "translations route disabled: no loaded structured_translation model", "route_disabled"), nil
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
	if !contract.AllowTypeText {
		return translationsError("invalid_request_error", "text translations are not allowed by the purpose contract", ""), nil
	}
	if e := validate.CheckLang(req.SourceLanguage, contract, contract.RequireSourceLangCode); e != nil {
		return translationsError(e.Type, e.Message, e.Code), nil
	}
	if e := validate.CheckLang(req.TargetLanguage, contract, contract.RequireTargetLangCode); e != nil {
		return translationsError(e.Type, e.Message, e.Code), nil
	}

	templateID := ""
	if contract.RequireChatTemplate {
		templateID = contract.ChatTemplateID
	}
	maxTokens := 4096
	if contract.MaxTokensDefault != nil {
		maxTokens = *contract.MaxTokensDefault
	}

	out := &TranslationsResponse{
		Object:       "translation.batch",
		Model:        catalogID,
		Translations: make([]TranslationItem, 0, len(req.Inputs)),
		Usage:        &TranslationUsage{},
	}
	for _, in := range req.Inputs {
		body, err := buildTranslatePrompt(req, in, contract, templateID, maxTokens)
		if err != nil {
			return translationsError("invalid_request_error", err.Error(), ""), nil
		}
		res, err := unixsvc.Chat(ctx, snap.NativeID, &unixsvc.ChatBody{Body: body})
		if err != nil || res == nil {
			rlog.Error("translations proxy failed", "model", catalogID, "input_id", in.ID, "err", err)
			return translationsError("server_error", "backend unavailable", "backend_unavailable"), nil
		}
		text, usage, errResp := parseWorkerTranslation(res.Body)
		if errResp != nil {
			return errResp, nil
		}
		out.Translations = append(out.Translations, TranslationItem{
			ID:     in.ID,
			Output: extractTranslationOutput(text),
		})
		if usage != nil {
			out.Usage.InputTokens += usage.InputTokens
			out.Usage.OutputTokens += usage.OutputTokens
			out.Usage.TotalTokens += usage.TotalTokens
		}
	}
	return out, nil
}

func buildTranslatePrompt(req *TranslationsRequest, in TranslationInput, c *control.PurposeStructuredTranslationContract, templateID string, maxTokens int) ([]byte, error) {
	textField := strings.TrimSpace(c.TextPayloadField)
	if textField == "" {
		textField = "text"
	}
	part := map[string]any{
		"type":             "text",
		"source_lang_code": strings.TrimSpace(req.SourceLanguage),
		"target_lang_code": strings.TrimSpace(req.TargetLanguage),
	}
	part[textField] = in.Text
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
	body := map[string]any{
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": []any{part},
			},
		},
		"max_tokens": maxTokens,
		"stream":     false,
	}
	if templateID != "" {
		body["chat_template_id"] = templateID
	}
	if c.TemperatureDefault != nil {
		body["temperature"] = *c.TemperatureDefault
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

var fenceRE = regexp.MustCompile("(?s)^\\s*```(?:json)?\\s*(.*?)\\s*```\\s*$")

func extractTranslationOutput(s string) string {
	s = strings.TrimSpace(s)
	if m := fenceRE.FindStringSubmatch(s); len(m) == 2 {
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

func parseWorkerTranslation(raw []byte) (string, *TranslationUsage, *TranslationsResponse) {
	var out struct {
		Content string `json:"content"`
		Usage   *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Error *OpenAIError `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", nil, translationsError("server_error", "invalid backend response", "")
	}
	if out.Error != nil && out.Error.Message != "" {
		return "", nil, translationsError(out.Error.Type, out.Error.Message, out.Error.Code)
	}
	usage := &TranslationUsage{}
	if out.Usage != nil {
		usage.InputTokens = out.Usage.InputTokens
		if usage.InputTokens == 0 {
			usage.InputTokens = out.Usage.PromptTokens
		}
		usage.OutputTokens = out.Usage.OutputTokens
		if usage.OutputTokens == 0 {
			usage.OutputTokens = out.Usage.CompletionTokens
		}
		usage.TotalTokens = out.Usage.TotalTokens
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
	}
	return out.Content, usage, nil
}

func translationsError(typ, message, code string) *TranslationsResponse {
	return &TranslationsResponse{Error: &OpenAIError{Type: typ, Message: message, Code: code}}
}
