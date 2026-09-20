package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"encore.app/control"
	"encore.dev/rlog"
)

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8"/>
  <title>API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"/>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "/openapi.json",
      dom_id: "#swagger-ui",
      presets: [SwaggerUIBundle.presets.apis],
      layout: "BaseLayout"
    });
  </script>
</body>
</html>
`

// Docs serves Swagger UI for the business (gateway) surface. Public; no auth.
//
//encore:api public raw method=GET path=/docs
func (s *Service) Docs(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIHTML))
}

// OpenAPI serves the OpenAPI 3 document for business routes. Public; no auth.
// Does not document gRPC, management Load/Unload, sockets, or secrets.
//
//encore:api public raw method=GET path=/openapi.json
func (s *Service) OpenAPI(w http.ResponseWriter, req *http.Request) {
	routes, err := control.ListRoutes(req.Context())
	if err != nil {
		rlog.Error("openapi list routes failed", "event", "gateway.openapi_failed", "err", err)
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	chatEnabled := false
	translationsEnabled := false
	for _, r := range routes.Routes {
		if r.Route == "POST /v1/chat/completions" && r.Purpose == "translation" && r.Enabled {
			chatEnabled = true
		}
		if r.Route == "POST /v1/translations" && r.Purpose == "structured_translation" && r.Enabled {
			translationsEnabled = true
		}
	}
	doc := buildOpenAPI(chatEnabled, translationsEnabled)
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		rlog.Error("openapi encode failed", "event", "gateway.openapi_encode_failed", "err", err)
	}
}

func buildOpenAPI(chatEnabled, translationsEnabled bool) map[string]any {
	chatDesc := "OpenAI-compatible chat completions for purpose=translation only (BabelDOC / HY-MT2). messages[].content is a string. Sampling: model, messages, temperature, top_p, max_tokens, stream. Defaults/max from purpose_translation."
	if !chatEnabled {
		chatDesc = "disabled: no loaded translation model. " + chatDesc
	}
	trDesc := "Document structured translation (TranslateGemma / purpose=structured_translation). Request uses source_language, target_language, optional context/glossaries, and inputs[]. Response is object=translation.batch with translations[].output and input_tokens/output_tokens."
	if !translationsEnabled {
		trDesc = "disabled: no loaded structured_translation model. " + trDesc
	}

	paths := map[string]any{
		"/v1/chat/completions": map[string]any{
			"post": map[string]any{
				"operationId": "chatCompletions",
				"summary":     "Chat Completions",
				"description": chatDesc,
				"deprecated":  !chatEnabled,
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":     "object",
								"required": []string{"model", "messages"},
								"properties": map[string]any{
									"model": map[string]any{
										"type":        "string",
										"description": "Catalog model id with purpose=translation (e.g. HY-MT2-7B-Q8_0)",
									},
									"messages": map[string]any{
										"type": "array",
										"items": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"role":    map[string]any{"type": "string"},
												"content": map[string]any{"type": "string"},
											},
										},
									},
									"temperature": map[string]any{"type": "number"},
									"top_p":       map[string]any{"type": "number"},
									"max_tokens":  map[string]any{"type": "integer"},
									"stream":      map[string]any{"type": "boolean"},
								},
							},
							"examples": map[string]any{
								"translation": map[string]any{
									"summary": "BabelDOC / HY-MT2 (purpose=translation)",
									"value": map[string]any{
										"model": "HY-MT2-7B-Q8_0",
										"messages": []any{
											map[string]any{"role": "user", "content": "Hello"},
										},
									},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Chat completion. Assistant content is a string. usage uses OpenAI prompt_tokens / completion_tokens.",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"choices": map[string]any{
											"type": "array",
											"items": map[string]any{
												"type": "object",
												"properties": map[string]any{
													"message": map[string]any{
														"type": "object",
														"properties": map[string]any{
															"role":    map[string]any{"type": "string"},
															"content": map[string]any{"type": "string"},
														},
													},
												},
											},
										},
										"usage": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"prompt_tokens":     map[string]any{"type": "integer"},
												"completion_tokens": map[string]any{"type": "integer"},
												"total_tokens":      map[string]any{"type": "integer"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"/v1/translations": map[string]any{
			"post": map[string]any{
				"operationId": "translations",
				"summary":     "Structured Translations",
				"description": trDesc,
				"deprecated":  !translationsEnabled,
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":     "object",
								"required": []string{"model", "source_language", "target_language", "inputs"},
								"properties": map[string]any{
									"model":            map[string]any{"type": "string"},
									"source_language":  map[string]any{"type": "string"},
									"target_language":  map[string]any{"type": "string"},
									"context": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"document_title": map[string]any{"type": "string"},
											"recent_title":   map[string]any{"type": "string"},
										},
									},
									"glossaries": map[string]any{
										"type": "array",
										"items": map[string]any{"type": "object"},
									},
									"inputs": map[string]any{
										"type": "array",
										"items": map[string]any{
											"type":     "object",
											"required": []string{"id", "text"},
											"properties": map[string]any{
												"id":                map[string]any{"type": "integer"},
												"text":              map[string]any{"type": "string"},
												"layout_label":      map[string]any{"type": "string"},
												"placeholder_hints": map[string]any{"type": "object"},
											},
										},
									},
								},
							},
							"examples": map[string]any{
								"batch": map[string]any{
									"summary": "Document batch (TranslateGemma)",
									"value": map[string]any{
										"model":           "translategemma-12b-it-6bit",
										"source_language": "en",
										"target_language": "zh",
										"context": map[string]any{
											"document_title": "Paper",
											"recent_title":   "1 Introduction",
										},
										"inputs": []any{
											map[string]any{
												"id":   0,
												"text": "We prove that the <style id='1'>verifier</style> preserves {v1}.",
											},
										},
									},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "translation.batch — translations aligned by id; usage uses input_tokens / output_tokens.",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"object": map[string]any{"type": "string", "enum": []string{"translation.batch"}},
										"model":  map[string]any{"type": "string"},
										"translations": map[string]any{
											"type": "array",
											"items": map[string]any{
												"type": "object",
												"properties": map[string]any{
													"id":     map[string]any{"type": "integer"},
													"output": map[string]any{"type": "string"},
												},
											},
										},
										"usage": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"input_tokens":  map[string]any{"type": "integer"},
												"output_tokens": map[string]any{"type": "integer"},
												"total_tokens":  map[string]any{"type": "integer"},
											},
										},
									},
								},
								"examples": map[string]any{
									"batch": map[string]any{
										"value": map[string]any{
											"object": "translation.batch",
											"model":  "translategemma-12b-it-6bit",
											"translations": []any{
												map[string]any{
													"id":     0,
													"output": "我们证明<style id='1'>验证器</style>保持了 {v1}。",
												},
											},
											"usage": map[string]any{
												"input_tokens":  82,
												"output_tokens": 31,
												"total_tokens":  113,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"/v1/models": map[string]any{
			"get": map[string]any{
				"operationId": "listModels",
				"summary":     "List Models",
				"description": "Loaded purpose=translation models only (BabelDOC). Structured models are not listed here; see /control/routes and POST /v1/translations.",
				"deprecated":  !chatEnabled,
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Model list",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object"},
							},
						},
					},
				},
			},
		},
		"/control/routes": map[string]any{
			"get": map[string]any{
				"operationId": "listRoutes",
				"summary":     "List API route enablement",
				"description": "Read-only enablement flags for OpenAI-compatible (route, purpose) pairs, including POST /v1/translations.",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Route list",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object"},
							},
						},
					},
				},
			},
		},
	}

	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Local LLM Gateway",
			"version":     "0.1.0",
			"description": "Business OpenAI-compatible HTTP on the gateway. Management APIs are not documented here.",
		},
		"servers": []map[string]any{
			{"url": "/", "description": "Encore local HTTP"},
		},
		"paths": paths,
	}
}

func openAPIContainsForbidden(raw []byte) bool {
	s := strings.ToLower(string(raw))
	forbidden := []string{
		"grpc",
		":9000",
		"loadmodel",
		"unloadmodel",
		"protobuf",
		".sock",
		"secret",
	}
	for _, f := range forbidden {
		if strings.Contains(s, f) {
			return true
		}
	}
	return false
}
