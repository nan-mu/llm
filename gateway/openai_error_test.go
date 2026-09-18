package gateway

import (
	"net/http"
	"testing"
)

func TestOpenAIErrorHTTPStatus(t *testing.T) {
	cases := []struct {
		code string
		typ  string
		want int
	}{
		{"model_not_loaded", "invalid_request_error", http.StatusBadRequest},
		{"route_disabled", "invalid_request_error", http.StatusBadRequest},
		{"model_not_found", "invalid_request_error", http.StatusNotFound},
		{"backend_unavailable", "server_error", http.StatusBadGateway},
		{"", "invalid_request_error", http.StatusBadRequest},
		{"", "server_error", http.StatusBadGateway},
	}
	for _, tc := range cases {
		got := openAIErrorHTTPStatus(&OpenAIError{Code: tc.code, Type: tc.typ})
		if got != tc.want {
			t.Fatalf("code=%q type=%q: got %d want %d", tc.code, tc.typ, got, tc.want)
		}
	}
}

func TestOpenAIErrorRetryable(t *testing.T) {
	if openAIErrorRetryable(&OpenAIError{Code: "model_not_loaded", Type: "invalid_request_error"}) {
		t.Fatal("model_not_loaded must not be retryable")
	}
	if openAIErrorRetryable(&OpenAIError{Code: "backend_unavailable", Type: "server_error"}) {
		t.Fatal("backend_unavailable must not invite client retries after gateway recovery")
	}
}
