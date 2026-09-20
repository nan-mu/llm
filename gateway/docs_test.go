package gateway

import (
	"encoding/json"
	"testing"
)

func TestOpenAPIOmitsForbidden(t *testing.T) {
	doc := buildOpenAPI(true, true)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if openAPIContainsForbidden(raw) {
		t.Fatalf("openapi leaked control-plane terms: %s", raw)
	}
}
