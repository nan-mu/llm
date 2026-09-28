package zotero

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	resp, err := (&Service{}).Health(context.Background())
	if err != nil || resp == nil || !resp.OK {
		t.Fatalf("health: %+v err=%v", resp, err)
	}
}

func TestSubmitRejectsNonPDFContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	(&Service{}).SubmitDocument(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestValidHash(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	h := hex.EncodeToString(sum[:])
	if !validHash(h) {
		t.Fatal("expected valid")
	}
	if validHash("abc") {
		t.Fatal("expected invalid")
	}
}
