package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"encore.app/control"
	"encore.app/internal/modelstate"
	"encore.dev/beta/errs"
)

const (
	// RouteChat is the catalog key for chat completions.
	RouteChat = "POST /v1/chat/completions"
	// RouteTranslations is the catalog key for structured translation.
	RouteTranslations = "POST /v1/translations"
)

// Message is one chat message. For purpose=translation, content is a JSON string.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Request is the chat body after sampling defaults are applied.
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

// Result is a successful chat validation.
type Result struct {
	Purpose string
}

// PurposeContract validates chat completions for purpose=translation.
// Sampling semantics: omitted field uses the table default; a value above max is rejected; a value at or below max is kept.
// structured_translation models are rejected with model_purpose_mismatch.
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
		if errs.Code(err) == errs.NotFound {
			return nil, invalid("model not found", "model_not_found")
		}
		return nil, server("catalog unavailable")
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
	if e := applyTranslation(req, contract); e != nil {
		return nil, e
	}
	return &Result{Purpose: string(purpose)}, nil
}

func applyTranslation(req *Request, c *control.PurposeTranslationContract) *Error {
	for _, m := range req.Messages {
		if !c.AllowMultimodalContent && !isJSONString(m.Content) {
			return invalid("translation message content must be a string", "")
		}
		role := strings.TrimSpace(m.Role)
		if role == "" {
			return invalid("message role required", "")
		}
		if role == "system" && !c.AllowSystemRole {
			return invalid("system role not allowed for translation", "")
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

// CheckLang checks a language code against the structured-translation contract.
func CheckLang(code string, c *control.PurposeStructuredTranslationContract, required bool) *Error {
	if c == nil {
		return server("purpose contract unavailable")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		if required {
			return invalid("language code required", "")
		}
		return nil
	}
	pat := strings.TrimSpace(c.LangCodePattern)
	if pat != "" {
		re, err := regexp.Compile(pat)
		if err != nil {
			return server("invalid lang_code_pattern in purpose contract")
		}
		if !re.MatchString(code) {
			return invalid("language code format invalid", "")
		}
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
