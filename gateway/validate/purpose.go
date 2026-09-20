package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"encore.app/control"
	"encore.app/internal/modelstate"
)

const (
	RouteChat         = "POST /v1/chat/completions"
	RouteTranslations = "POST /v1/translations"
)

// Message is one chat message; content must be a JSON string for translation chat.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Request is the mutable chat body validated and rewritten by PurposeContract.
type Request struct {
	Model             string    `json:"model"`
	Messages          []Message `json:"messages"`
	Temperature       *float64  `json:"temperature,omitempty"`
	TopP              *float64  `json:"top_p,omitempty"`
	TopK              *int      `json:"top_k,omitempty"`
	RepetitionPenalty *float64  `json:"repetition_penalty,omitempty"`
	MaxTokens         *int      `json:"max_tokens,omitempty"`
	Stream            bool      `json:"stream,omitempty"`
}

// Result is a successful PurposeContract outcome (translation chat only).
type Result struct {
	Purpose string
}

// PurposeContract validates chat completions for purpose=translation only.
// structured_translation must use POST /v1/translations.
func PurposeContract(ctx context.Context, req *Request) (*Result, *Error) {
	if req == nil || strings.TrimSpace(req.Model) == "" {
		return nil, invalid("model required", "")
	}
	if len(req.Messages) == 0 {
		return nil, invalid("messages required", "")
	}
	if req.Stream {
		return nil, invalid("streaming not supported", "")
	}

	catalogID := strings.TrimSpace(req.Model)
	snap, err := control.GetSnapshot(ctx, catalogID)
	if err != nil {
		return nil, invalid("model not found", "model_not_found")
	}
	purpose := modelstate.Purpose(snap.Purpose)
	if purpose != modelstate.PurposeTranslation {
		return nil, invalid("model purpose does not match chat completions", "model_purpose_mismatch")
	}

	enabled, err := control.RouteEnabled(ctx, &control.RouteEnabledParams{
		Route:   RouteChat,
		Purpose: string(purpose),
	})
	if err != nil {
		return nil, server("route check failed")
	}
	if !enabled.Enabled {
		return nil, invalid("chat route disabled: no loaded translation model", "route_disabled")
	}

	contract, err := control.GetPurposeTranslation(ctx)
	if err != nil {
		return nil, server("purpose contract unavailable")
	}
	if e := validateTranslation(req, contract); e != nil {
		return nil, e
	}
	return &Result{Purpose: string(purpose)}, nil
}

func validateTranslation(req *Request, c *control.PurposeTranslationContract) *Error {
	for _, m := range req.Messages {
		role := strings.TrimSpace(m.Role)
		if role == "system" && !c.AllowSystemRole {
			return invalid("system role not allowed for translation", "")
		}
		if !isJSONString(m.Content) {
			return invalid("translation message content must be a string", "")
		}
	}
	if e := applyFloat(&req.Temperature, c.TemperatureDefault, c.TemperatureMax, "temperature"); e != nil {
		return e
	}
	if e := applyFloat(&req.TopP, c.TopPDefault, c.TopPMax, "top_p"); e != nil {
		return e
	}
	if e := applyInt(&req.MaxTokens, c.MaxTokensDefault, c.MaxTokensMax, "max_tokens"); e != nil {
		return e
	}
	if c.InjectTopK {
		if e := applyInt(&req.TopK, c.TopKDefault, c.TopKMax, "top_k"); e != nil {
			return e
		}
	} else if req.TopK != nil && *req.TopK > c.TopKMax {
		return invalid("top_k exceeds max for translation", "")
	}
	if c.InjectRepetitionPenalty {
		if e := applyFloat(&req.RepetitionPenalty, c.RepetitionPenaltyDefault, c.RepetitionPenaltyMax, "repetition_penalty"); e != nil {
			return e
		}
	} else if req.RepetitionPenalty != nil && *req.RepetitionPenalty > c.RepetitionPenaltyMax {
		return invalid("repetition_penalty exceeds max for translation", "")
	}
	return nil
}

// LangOK reports whether code matches the structured purpose lang pattern.
func LangOK(ctx context.Context, code string) *Error {
	c, err := control.GetPurposeStructuredTranslation(ctx)
	if err != nil {
		return server("purpose contract unavailable")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return invalid("language code required", "")
	}
	pat := strings.TrimSpace(c.LangCodePattern)
	if pat == "" {
		return nil
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return server("invalid lang_code_pattern in purpose contract")
	}
	if !re.MatchString(code) {
		return invalid("language code format invalid", "")
	}
	if len(c.SupportedLangCodes) > 0 {
		ok := false
		for _, s := range c.SupportedLangCodes {
			if s == code {
				ok = true
				break
			}
		}
		if !ok {
			return invalid("language code not in supported_lang_codes", "")
		}
	}
	return nil
}

func isJSONString(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '"'
}

func applyFloat(ptr **float64, def, max float64, name string) *Error {
	if *ptr == nil {
		v := def
		*ptr = &v
		return nil
	}
	if **ptr > max {
		return invalid(name+" exceeds max for translation", "")
	}
	return nil
}

func applyInt(ptr **int, def, max int, name string) *Error {
	if *ptr == nil {
		v := def
		*ptr = &v
		return nil
	}
	if **ptr > max {
		return invalid(name+" exceeds max for translation", "")
	}
	return nil
}
