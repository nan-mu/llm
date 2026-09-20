package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"encore.app/internal/modelstate"
	"encore.app/frontend"
)

const (
	pathModels       = "/models"
	pathModelsLoad   = "/models/load"
	pathModelsUnload = "/models/unload"
	pathHealth       = "/health"
	defaultWaitPoll  = 100 * time.Millisecond
)

// Client talks HTTP to llama-server / mlxcel-server. It is not a control-plane catalog.
type Client struct {
	socket     string
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewUnix dials a Unix domain socket.
func NewUnix(socket, apiKey string) *Client {
	socket = strings.TrimSpace(socket)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &Client{
		socket:  socket,
		baseURL: "http://localhost",
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:   0,
			Transport: transport,
		},
	}
}

// NewHTTP is for tests against httptest.Server.
func NewHTTP(baseURL, apiKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 0}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: hc,
	}
}

func (c *Client) Socket() string { return c.socket }

func (c *Client) Healthy(ctx context.Context) error {
	if _, err := c.doRaw(ctx, http.MethodGet, pathHealth, nil); err == nil {
		return nil
	}
	_, err := c.ListModels(ctx)
	return err
}

func (c *Client) ListModels(ctx context.Context) ([]frontend.Model, error) {
	return c.listModels(ctx, pathModels)
}

// ReloadModels rescans --models-dir. llama-server indexes that directory at start
// (and when the process is reused via an existing socket); files added later are
// invisible to POST /models/load until this refresh.
func (c *Client) ReloadModels(ctx context.Context) error {
	_, err := c.listModels(ctx, pathModels+"?reload=1")
	return err
}

func (c *Client) listModels(ctx context.Context, path string) ([]frontend.Model, error) {
	var payload modelsListResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return nil, frontend.WrapUnavailable("list models", err)
	}
	out := make([]frontend.Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		out = append(out, item.toModel())
	}
	return out, nil
}

func (c *Client) GetModel(ctx context.Context, id string) (frontend.Model, error) {
	models, err := c.ListModels(ctx)
	if err != nil {
		return frontend.Model{}, err
	}
	for _, m := range models {
		if m.ID == id {
			return m, nil
		}
	}
	return frontend.Model{}, &frontend.Error{Code: frontend.CodeNotFound, Message: "model is not found", StatusCode: http.StatusNotFound}
}

func (c *Client) LoadModel(ctx context.Context, id string) error {
	body := map[string]string{"model": id}
	if err := c.doJSON(ctx, http.MethodPost, pathModelsLoad, body, nil); err != nil {
		return mapMutationError(frontend.CodeLoadFailed, err)
	}
	return nil
}

func (c *Client) UnloadModel(ctx context.Context, id string) error {
	body := map[string]string{"model": id}
	if err := c.doJSON(ctx, http.MethodPost, pathModelsUnload, body, nil); err != nil {
		return mapMutationError(frontend.CodeUnloadFailed, err)
	}
	return nil
}

// WaitUntilLoaded polls GetModel until loaded or failed.
func WaitUntilLoaded(ctx context.Context, c *Client, name string) (frontend.Model, error) {
	return waitForStates(ctx, c, name, true)
}

// WaitUntilUnloaded polls GetModel until unloaded or failed.
func WaitUntilUnloaded(ctx context.Context, c *Client, name string) (frontend.Model, error) {
	return waitForStates(ctx, c, name, false)
}

func waitForStates(ctx context.Context, c *Client, name string, wantLoaded bool) (frontend.Model, error) {
	ticker := time.NewTicker(defaultWaitPoll)
	defer ticker.Stop()
	for {
		info, err := c.GetModel(ctx, name)
		if err != nil {
			return frontend.Model{}, err
		}
		if info.State == modelstate.ModelFailed ||
			(wantLoaded && info.State == modelstate.ModelLoaded) ||
			(!wantLoaded && info.State == modelstate.ModelUnloaded) {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return info, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, reqBody any, out any) error {
	raw, err := c.doRaw(ctx, method, path, reqBody)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &frontend.Error{Code: frontend.CodeBackendUnavailable, Message: "decode backend response: " + err.Error(), Err: err}
	}
	return nil
}

func (c *Client) doRaw(ctx context.Context, method, path string, reqBody any) ([]byte, error) {
	var rdr io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &frontend.Error{Code: frontend.CodeBackendUnavailable, Message: err.Error(), Err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &frontend.Error{Code: frontend.CodeBackendUnavailable, Message: err.Error(), Err: err}
	}
	if resp.StatusCode >= 400 {
		return nil, decodeAPIError(resp.StatusCode, body)
	}
	return body, nil
}

func mapMutationError(fallback string, err error) error {
	var e *frontend.Error
	if errors.As(err, &e) {
		msg := strings.ToLower(e.Message)
		switch {
		case e.StatusCode == http.StatusNotFound || strings.Contains(msg, "not found"):
			return &frontend.Error{Code: frontend.CodeNotFound, Message: e.Message, StatusCode: e.StatusCode, Err: e}
		case strings.Contains(msg, "already running") || strings.Contains(msg, "already loaded"):
			return &frontend.Error{Code: frontend.CodeAlreadyRunning, Message: e.Message, StatusCode: e.StatusCode, Err: e}
		case e.Code == frontend.CodeBackendUnavailable:
			return e
		}
		return &frontend.Error{Code: fallback, Message: e.Message, StatusCode: e.StatusCode, Err: e}
	}
	return frontend.WrapUnavailable(fallback, err)
}

func decodeAPIError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	var parsed struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && len(parsed.Error) > 0 {
		var asString string
		var asObj struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		}
		if json.Unmarshal(parsed.Error, &asString) == nil && asString != "" {
			msg = asString
		} else if json.Unmarshal(parsed.Error, &asObj) == nil && asObj.Message != "" {
			msg = asObj.Message
		}
	}
	code := frontend.CodeBackendUnavailable
	switch {
	case status == http.StatusNotFound:
		code = frontend.CodeNotFound
	case status >= 500:
		code = frontend.CodeBackendUnavailable
	default:
		code = frontend.CodeLoadFailed
	}
	return &frontend.Error{Code: code, Message: msg, StatusCode: status}
}
