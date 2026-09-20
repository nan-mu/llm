package gateway

import (
	"encoding/json"
	"testing"
)

func TestBuildTranslateGemmaChatBodyKeepsTextClean(t *testing.T) {
	t.Parallel()
	req := &TranslationsRequest{
		Model:          "translategemma-12b-it-6bit",
		SourceLanguage: "en",
		TargetLanguage: "zh",
		Context: &TranslationContext{
			DocumentTitle: "Paper",
			RecentTitle:   "Intro",
		},
		Glossaries: []Glossary{{
			Name:    "terms",
			Entries: []GlossaryEntry{{Source: "verifier", Target: "验证器"}},
		}},
		Inputs: nil,
	}
	in := TranslationInput{
		ID:          0,
		Text:        "We prove the verifier preserves {v1}.",
		LayoutLabel: "paragraph",
		PlaceholderHints: map[string]string{
			"{v1}": "invariant",
		},
	}
	raw, err := buildTranslateGemmaChatBody(req, in, nil, 4096, "translategemma")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["chat_template_id"] != "translategemma" {
		t.Fatalf("chat_template_id: %#v", body["chat_template_id"])
	}
	if _, ok := body["model"]; ok {
		t.Fatal("prompt JSON must not include model id")
	}
	msgs := body["messages"].([]any)
	msg := msgs[0].(map[string]any)
	parts := msg["content"].([]any)
	part := parts[0].(map[string]any)
	if part["text"] != in.Text {
		t.Fatalf("text must be source-only, got %#v", part["text"])
	}
	if part["document_title"] != "Paper" {
		t.Fatalf("document_title: %#v", part["document_title"])
	}
	if part["layout_label"] != "paragraph" {
		t.Fatalf("layout_label: %#v", part["layout_label"])
	}
	gloss := part["glossary"].([]any)
	if len(gloss) != 1 {
		t.Fatalf("glossary: %#v", gloss)
	}
}

func TestExtractTranslationOutputStripsFences(t *testing.T) {
	t.Parallel()
	in := "```json\n[{\"type\":\"text\",\"source_lang_code\":\"en\",\"target_lang_code\":\"zh\",\"text\":\"我们证明<style id='1'>验证器</style>保持了 {v1}。\"}]\n```"
	got := extractTranslationOutput(in)
	want := "我们证明<style id='1'>验证器</style>保持了 {v1}。"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractTranslationOutputPlainString(t *testing.T) {
	t.Parallel()
	in := "生成的代码满足以下性质。"
	if got := extractTranslationOutput(in); got != in {
		t.Fatalf("got %q", got)
	}
}

func TestExtractTranslationOutputObjectText(t *testing.T) {
	t.Parallel()
	in := "```\n{\"text\":\"你好\"}\n```"
	if got := extractTranslationOutput(in); got != "你好" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateTranslationsRequestRejectsEmpty(t *testing.T) {
	t.Parallel()
	if e := validateTranslationsRequest(nil, nil); e == nil || e.Error == nil {
		t.Fatal("expected error")
	}
	req := &TranslationsRequest{Model: "x", SourceLanguage: "en", TargetLanguage: "zh"}
	if e := validateTranslationsRequest(nil, req); e == nil || e.Error == nil {
		t.Fatal("expected inputs required")
	}
	req.Inputs = []TranslationInput{{ID: 0, Text: ""}}
	if e := validateTranslationsRequest(nil, req); e == nil || e.Error == nil {
		t.Fatal("expected text required")
	}
}
