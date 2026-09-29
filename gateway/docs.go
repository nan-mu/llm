package gateway

import (
	"encoding/json"
	"net/http"

	"encore.app/control"
	"encore.dev/rlog"
)

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8"/>
  <title>llm API</title>
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

// Docs serves Swagger UI for the public HTTP surface. No auth in this slice.
//
//encore:api public raw method=GET path=/docs
func Docs(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIHTML))
}

// OpenAPI serves the operator OpenAPI document for public routes.
// It does not document Load/Unload, sockets, or secrets.
// Private service APIs are in Encore's generated API schema on the local dashboard.
//
//encore:api public raw method=GET path=/openapi.json
func OpenAPI(w http.ResponseWriter, req *http.Request) {
	chatEnabled := false
	translationsEnabled := false
	routes, err := control.ListRoutes(req.Context())
	if err != nil {
		rlog.Error("openapi list routes failed", "err", err)
	} else {
		for _, r := range routes.Routes {
			if r.Route == "POST /v1/chat/completions" && r.Purpose == "translation" && r.Enabled {
				chatEnabled = true
			}
			if r.Route == "POST /v1/translations" && r.Purpose == "structured_translation" && r.Enabled {
				translationsEnabled = true
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(buildOpenAPI(chatEnabled, translationsEnabled)); err != nil {
		rlog.Error("openapi encode failed", "err", err)
	}
}

func buildOpenAPI(chatEnabled, translationsEnabled bool) map[string]any {
	chatDesc := "Chat completions for purpose=translation. messages[].content is a string. Sampling fields: temperature, top_p, max_tokens, stream. Defaults and maximums come from purpose_translation. structured_translation models return model_purpose_mismatch."
	if !chatEnabled {
		chatDesc = "disabled: no loaded translation model. " + chatDesc
	}
	trDesc := "Structured translation for purpose=structured_translation. Response object is translation.batch. Sampling defaults are applied server-side from purpose_structured_translation."
	if !translationsEnabled {
		trDesc = "disabled: no loaded structured_translation model. " + trDesc
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "llm",
			"version":     "0.1.0",
			"description": "Public HTTP surface. BabelDOC worker execution is not wired. Private Encore APIs are omitted here.",
		},
		"paths": map[string]any{
			"/health": map[string]any{
				"get": op("Gateway health", "Returns ok when the gateway is up.", "", map[string]any{
					"200": jsonResp("Health", "Gateway is up."),
				}),
			},
			"/v1/models": map[string]any{
				"get": op("List models", "Loaded translation and structured_translation models. ASR is omitted.", "", map[string]any{
					"200": jsonResp("ModelList", "Model list."),
				}),
			},
			"/v1/chat/completions": map[string]any{
				"post": op("Chat completions", chatDesc, "ChatCompletionsRequest", map[string]any{
					"200": jsonResp("ChatCompletionsResponse", "Completion or error envelope."),
					"400": jsonResp("ChatCompletionsResponse", "Catalog or validation failure. X-Should-Retry: false."),
					"404": jsonResp("ChatCompletionsResponse", "Unknown model."),
				}),
			},
			"/v1/translations": map[string]any{
				"post": op("Translations", trDesc, "TranslationsRequest", map[string]any{
					"200": jsonResp("TranslationsResponse", "translation.batch or error envelope."),
					"400": jsonResp("TranslationsResponse", "Catalog or validation failure. X-Should-Retry: false."),
					"404": jsonResp("TranslationsResponse", "Unknown model."),
				}),
			},
			"/v1/health": map[string]any{
				"get": op("Zotero health", "Zotero facade health. No auth in this slice.", "", map[string]any{
					"200": jsonResp("Health", "Facade is up."),
				}),
			},
			"/v1/documents": map[string]any{
				"get": op("List documents", "Full translation task list. Statuses: pending, down, error.", "", map[string]any{
					"200": jsonResp("DocumentList", "Tasks."),
				}),
				"post": map[string]any{
					"summary":     "Submit document",
					"description": "Raw application/pdf body. Optional X-Document-SHA256 must be 64 lowercase hex and match the bytes. Task id is the SHA-256 of the PDF. Does not run a PDF worker.",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/pdf": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}},
						},
					},
					"responses": map[string]any{
						"202": jsonResp("DocumentRef", "Task created (pending)."),
						"200": jsonResp("DocumentRef", "Task already exists."),
						"400": jsonResp("APIError", "Invalid PDF or hash."),
					},
				},
			},
			"/v1/documents/{hash}": map[string]any{
				"delete": withParams(op("Delete document", "Idempotent task cleanup.", "", map[string]any{
					"200": jsonResp("Ack", "Deleted or already absent."),
					"400": jsonResp("APIError", "Hash is not 64 lowercase hex."),
				}), hashParam()),
			},
			"/v1/documents/{hash}/files/dual": map[string]any{
				"get": map[string]any{
					"summary":     "Download dual PDF",
					"description": "Bilingual PDF when status is down. Sets X-Artifact-SHA256.",
					"parameters":  []any{hashParam()},
					"responses": map[string]any{
						"200": map[string]any{"description": "PDF bytes.", "content": map[string]any{"application/pdf": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}},
						"404": jsonResp("APIError", "Unknown task."),
						"409": jsonResp("APIError", "Task is not down."),
					},
				},
			},
			"/v1/documents/{hash}/retry": map[string]any{
				"post": withParams(op("Retry document", "Re-queues an error task. pending and down are idempotent. Does not run a PDF worker.", "", map[string]any{
					"202": jsonResp("DocumentRef", "Re-queued."),
					"200": jsonResp("DocumentRef", "Already pending or down."),
					"404": jsonResp("APIError", "Unknown task."),
				}), hashParam()),
			},
			"/control/health": map[string]any{
				"get": op("Control health", "Control plane and catalog database are up.", "", map[string]any{
					"200": jsonResp("Health", "Control is up."),
				}),
			},
			"/control/routes": map[string]any{
				"get": op("List routes", "OpenAI route enablement. Enabled is true only when a model of that purpose is observed loaded.", "", map[string]any{
					"200": jsonResp("RouteList", "Route table."),
				}),
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"Health":      map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}},
				"ChatMessage": map[string]any{"type": "object", "required": []string{"role", "content"}, "properties": map[string]any{"role": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}},
				"ChatCompletionsRequest": map[string]any{"type": "object", "required": []string{"model", "messages"}, "properties": map[string]any{
					"model":       map[string]any{"type": "string", "description": "Catalog id."},
					"messages":    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/ChatMessage"}},
					"temperature": map[string]any{"type": "number"},
					"top_p":       map[string]any{"type": "number"},
					"max_tokens":  map[string]any{"type": "integer"},
					"stream":      map[string]any{"type": "boolean"},
				}},
				"OpenAIError": map[string]any{"type": "object", "properties": map[string]any{
					"message": map[string]any{"type": "string"},
					"type":    map[string]any{"type": "string"},
					"code":    map[string]any{"type": "string"},
				}},
				"ChatCompletionsResponse": map[string]any{"type": "object", "properties": map[string]any{
					"id":      map[string]any{"type": "string"},
					"object":  map[string]any{"type": "string"},
					"created": map[string]any{"type": "integer"},
					"model":   map[string]any{"type": "string"},
					"choices": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
					"usage":   map[string]any{"type": "object"},
					"error":   map[string]any{"$ref": "#/components/schemas/OpenAIError"},
				}},
				"TranslationsRequest": map[string]any{"type": "object", "required": []string{"model", "source_language", "target_language", "inputs"}, "properties": map[string]any{
					"model":           map[string]any{"type": "string"},
					"source_language": map[string]any{"type": "string"},
					"target_language": map[string]any{"type": "string"},
					"context":         map[string]any{"type": "object"},
					"glossaries":      map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
					"inputs": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"id", "text"}, "properties": map[string]any{
						"id":                map[string]any{"type": "integer"},
						"text":              map[string]any{"type": "string"},
						"layout_label":      map[string]any{"type": "string"},
						"placeholder_hints": map[string]any{"type": "object"},
					}}},
				}},
				"TranslationsResponse": map[string]any{"type": "object", "properties": map[string]any{
					"object": map[string]any{"type": "string", "example": "translation.batch"},
					"model":  map[string]any{"type": "string"},
					"translations": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
						"id":     map[string]any{"type": "integer"},
						"output": map[string]any{"type": "string"},
					}}},
					"usage": map[string]any{"type": "object", "properties": map[string]any{
						"input_tokens":  map[string]any{"type": "integer"},
						"output_tokens": map[string]any{"type": "integer"},
						"total_tokens":  map[string]any{"type": "integer"},
					}},
					"error": map[string]any{"$ref": "#/components/schemas/OpenAIError"},
				}},
				"ModelList": map[string]any{"type": "object", "properties": map[string]any{
					"object": map[string]any{"type": "string"},
					"data":   map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
				}},
				"DocumentRef": map[string]any{"type": "object", "properties": map[string]any{
					"id":     map[string]any{"type": "string"},
					"status": map[string]any{"type": "string", "enum": []string{"pending", "down", "error"}},
				}},
				"DocumentList": map[string]any{"type": "object", "properties": map[string]any{
					"documents": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/DocumentRef"}},
				}},
				"Ack": map[string]any{"type": "object", "properties": map[string]any{"ack": map[string]any{"type": "boolean"}}},
				"APIError": map[string]any{"type": "object", "properties": map[string]any{
					"error": map[string]any{"type": "object", "properties": map[string]any{
						"code":    map[string]any{"type": "string"},
						"message": map[string]any{"type": "string"},
					}},
				}},
				"RouteList": map[string]any{"type": "object", "properties": map[string]any{
					"routes": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
						"route":   map[string]any{"type": "string"},
						"purpose": map[string]any{"type": "string"},
						"enabled": map[string]any{"type": "boolean"},
					}}},
				}},
			},
		},
	}
}

func op(summary, description, requestSchema string, responses map[string]any) map[string]any {
	out := map[string]any{
		"summary":     summary,
		"description": description,
		"responses":   responses,
	}
	if requestSchema != "" {
		out["requestBody"] = map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/" + requestSchema},
				},
			},
		}
	}
	return out
}

func withParams(op map[string]any, params ...any) map[string]any {
	op["parameters"] = params
	return op
}

func hashParam() map[string]any {
	return map[string]any{
		"name":     "hash",
		"in":       "path",
		"required": true,
		"schema":   map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"},
	}
}

func jsonResp(schema, description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}
