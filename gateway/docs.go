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
	byRoute := map[string]control.RouteInfo{}
	for _, r := range routes.Routes {
		byRoute[r.Route] = r
	}
	doc := buildOpenAPI(byRoute)
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		rlog.Error("openapi encode failed", "event", "gateway.openapi_encode_failed", "err", err)
	}
}

func buildOpenAPI(byRoute map[string]control.RouteInfo) map[string]any {
	chat := byRoute["POST /v1/chat/completions"]
	chatDesc := "OpenAI-compatible chat completions. Route by request.model (catalog id) to a translation purpose model."
	if !chat.Enabled {
		chatDesc = "disabled: no loaded translation model. " + chatDesc
	}

	paths := map[string]any{
		"/v1/chat/completions": map[string]any{
			"post": map[string]any{
				"operationId": "chatCompletions",
				"summary":     "Chat Completions",
				"description": chatDesc,
				"deprecated":  !chat.Enabled,
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
										"description": "Catalog model id (e.g. translategemma-12b-it-6bit)",
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
									"max_tokens":  map[string]any{"type": "integer"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Chat completion",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object"},
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
				"description": "Loaded translation models visible when the chat route is enabled.",
				"deprecated":  !chat.Enabled,
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
				"description": "Read-only enablement flags for OpenAI-compatible routes.",
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
