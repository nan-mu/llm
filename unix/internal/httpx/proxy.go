package httpx

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ReverseProxyUnix forwards an inbound HTTP request to a Unix-socket backend.
// The client Timeout is 0 so long-running chat completions are not cut short.
func ReverseProxyUnix(socket, apiKey string, w http.ResponseWriter, req *http.Request) error {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		http.Error(w, `{"error":{"message":"no backend socket","type":"server_error"}}`, http.StatusBadGateway)
		return nil
	}
	resp, err := doUnix(req.Context(), socket, apiKey, req.Method, requestPath(req), req.Body, req.Header, req.ContentLength)
	if err != nil {
		http.Error(w, `{"error":{"message":"backend unavailable","type":"server_error"}}`, http.StatusBadGateway)
		return nil
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return nil
}

// DoUnixJSON POSTs JSON to a Unix-socket backend and returns status + body.
// Timeout is 0 so long-running chat completions are not cut short.
func DoUnixJSON(ctx context.Context, socket, apiKey, method, path string, body []byte) (status int, respBody []byte, err error) {
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	resp, err := doUnix(ctx, socket, apiKey, method, path, bytes.NewReader(body), hdr, int64(len(body)))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, respBody, nil
}

func requestPath(req *http.Request) string {
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/v1/chat/completions"
	}
	if req.URL.RawQuery != "" {
		path += "?" + req.URL.RawQuery
	}
	return path
}

func doUnix(ctx context.Context, socket, apiKey, method, path string, body io.Reader, hdr http.Header, contentLength int64) (*http.Response, error) {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		return nil, errNoSocket
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "unix", socket)
		},
	}
	client := &http.Client{Timeout: 0, Transport: transport}
	out, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, body)
	if err != nil {
		return nil, err
	}
	for k, vv := range hdr {
		lk := strings.ToLower(k)
		if lk == "content-length" || lk == "host" || lk == "transfer-encoding" {
			continue
		}
		for _, v := range vv {
			out.Header.Add(k, v)
		}
	}
	if contentLength >= 0 {
		out.ContentLength = contentLength
	}
	if apiKey != "" && out.Header.Get("Authorization") == "" {
		out.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return client.Do(out)
}

type socketError string

func (e socketError) Error() string { return string(e) }

const errNoSocket = socketError("no backend socket")
