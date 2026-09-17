package httpx_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"encore.app/internal/modelstate"
	"encore.app/unix"
	"encore.app/unix/internal/httpx"
)

func TestHTTPClientListLoadUnload(t *testing.T) {
	t.Parallel()

	models := []map[string]any{
		{
			"id":   "qwen-small",
			"path": "/models/qwen-small.gguf",
			"status": map[string]any{
				"value": "unloaded",
			},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/models/load", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "qwen-small" {
			http.Error(w, `{"error":{"message":"model is not found"}}`, http.StatusNotFound)
			return
		}
		models[0]["status"] = map[string]any{"value": "loaded"}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/models/unload", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		models[0]["status"] = map[string]any{"value": "unloaded"}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": models})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := httpx.NewHTTP(srv.URL, "internal-key", srv.Client())
	ctx := context.Background()

	listed, err := c.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "qwen-small" || listed[0].State != modelstate.ModelUnloaded {
		t.Fatalf("list = %+v", listed)
	}

	if _, err := c.GetModel(ctx, "missing"); !unix.Is(err, unix.CodeNotFound) {
		t.Fatalf("show missing: %v", err)
	}

	if err := c.LoadModel(ctx, "qwen-small"); err != nil {
		t.Fatal(err)
	}
	info, err := c.GetModel(ctx, "qwen-small")
	if err != nil {
		t.Fatal(err)
	}
	if info.State != modelstate.ModelLoaded {
		t.Fatalf("state after load = %s", info.State)
	}

	if err := c.UnloadModel(ctx, "qwen-small"); err != nil {
		t.Fatal(err)
	}
	info, err = c.GetModel(ctx, "qwen-small")
	if err != nil {
		t.Fatal(err)
	}
	if info.State != modelstate.ModelUnloaded {
		t.Fatalf("state after unload = %s", info.State)
	}
}

func TestHTTPClientMapsAlreadyRunning(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model is already running"}}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	c := httpx.NewHTTP(srv.URL, "", srv.Client())
	err := c.LoadModel(context.Background(), "qwen-small")
	if !unix.Is(err, unix.CodeAlreadyRunning) {
		t.Fatalf("got %v", err)
	}
}

func TestHTTPClientUnavailable(t *testing.T) {
	t.Parallel()
	c := httpx.NewHTTP("http://127.0.0.1:1", "", &http.Client{})
	_, err := c.ListModels(context.Background())
	if !unix.Is(err, unix.CodeBackendUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestSleepingMapsToLoaded(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":     "qwen-small",
					"status": map[string]any{"value": "sleeping"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := httpx.NewHTTP(srv.URL, "", srv.Client())
	info, err := c.GetModel(context.Background(), "qwen-small")
	if err != nil {
		t.Fatal(err)
	}
	if info.State != modelstate.ModelLoaded {
		t.Fatalf("sleeping mapped to %s", info.State)
	}
}

func assertAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer internal-key" {
		t.Fatalf("authorization = %q", got)
	}
}
