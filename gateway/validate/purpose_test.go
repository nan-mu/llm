package validate

import (
	"encoding/json"
	"testing"

	"encore.app/control"
)

func TestApplyFloatSamplingA(t *testing.T) {
	t.Parallel()
	var p *float64
	if e := applyFloat(&p, 0.7, 0.7, "temperature"); e != nil {
		t.Fatal(e)
	}
	if p == nil || *p != 0.7 {
		t.Fatalf("want default 0.7, got %v", p)
	}
	ok := 0.7
	p = &ok
	if e := applyFloat(&p, 0.7, 0.7, "temperature"); e != nil {
		t.Fatal(e)
	}
	bad := 0.9
	p = &bad
	if e := applyFloat(&p, 0.7, 0.7, "temperature"); e == nil {
		t.Fatal("expected error for temperature > max")
	}
}

func TestTranslationRejectsNonStringContent(t *testing.T) {
	t.Parallel()
	req := &Request{
		Model: "x",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hi"}]`)},
		},
	}
	c := &control.PurposeTranslationContract{
		AllowSystemRole:          true,
		TemperatureDefault:       0.7,
		TemperatureMax:           0.7,
		TopPDefault:              0.6,
		TopPMax:                  0.6,
		TopKDefault:              20,
		TopKMax:                  20,
		RepetitionPenaltyDefault: 1.05,
		RepetitionPenaltyMax:     1.05,
		MaxTokensDefault:         4096,
		MaxTokensMax:             4096,
		InjectTopK:               true,
		InjectRepetitionPenalty:  true,
	}
	if e := validateTranslation(req, c); e == nil {
		t.Fatal("expected rejection of array content")
	}
}

func TestTranslationAcceptsStringAndFillsDefaults(t *testing.T) {
	t.Parallel()
	req := &Request{
		Model: "x",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
		},
	}
	c := &control.PurposeTranslationContract{
		AllowSystemRole:          true,
		TemperatureDefault:       0.7,
		TemperatureMax:           0.7,
		TopPDefault:              0.6,
		TopPMax:                  0.6,
		TopKDefault:              20,
		TopKMax:                  20,
		RepetitionPenaltyDefault: 1.05,
		RepetitionPenaltyMax:     1.05,
		MaxTokensDefault:         4096,
		MaxTokensMax:             4096,
		InjectTopK:               true,
		InjectRepetitionPenalty:  true,
	}
	if e := validateTranslation(req, c); e != nil {
		t.Fatal(e)
	}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Fatalf("temperature default: %v", req.Temperature)
	}
	if req.TopK == nil || *req.TopK != 20 {
		t.Fatalf("top_k default: %v", req.TopK)
	}
}

func TestAPITokenNoop(t *testing.T) {
	t.Parallel()
	if e := APIToken(""); e != nil {
		t.Fatal(e)
	}
	if e := APIToken("Bearer sk-local-x"); e != nil {
		t.Fatal(e)
	}
}
