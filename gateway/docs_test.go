package gateway

import (
	"encoding/json"
	"testing"

	"encore.app/control"
)

func TestOpenAPIOmitsForbidden(t *testing.T) {
	doc := buildOpenAPI(map[string]control.RouteInfo{
		"POST /v1/chat/completions": {
			Route:   "POST /v1/chat/completions",
			Purpose: "translation",
			Enabled: true,
		},
	})
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if openAPIContainsForbidden(raw) {
		t.Fatalf("openapi leaked control-plane terms: %s", raw)
	}
}
