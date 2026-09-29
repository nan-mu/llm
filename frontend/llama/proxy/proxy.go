// Package proxy dials llama's HTTP-over-UDS chat endpoint.
// It does not start the frontend. Gateway may import this package; it must not import frontend Start.
package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"encore.app/internal/approot"
)

// Post sends one chat JSON body to the llama worker socket and returns the HTTP status and body.
func Post(ctx context.Context, catalogSocket, nativeID string, body []byte) (int, []byte, error) {
	root, err := approot.Root()
	if err != nil {
		return 0, nil, err
	}
	sock, err := socketPath(root, catalogSocket, nativeID)
	if err != nil {
		return 0, nil, err
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func socketPath(root, catalogSocket, nativeID string) (string, error) {
	catalogSocket = strings.TrimSpace(catalogSocket)
	nativeID = strings.TrimSpace(nativeID)
	if catalogSocket == "" || nativeID == "" {
		return "", fmt.Errorf("llama socket path missing")
	}
	if strings.Contains(nativeID, "/") || strings.Contains(nativeID, "..") {
		return "", fmt.Errorf("invalid native id")
	}
	p := catalogSocket
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, catalogSocket)
	}
	if strings.HasSuffix(p, ".sock") {
		return p, nil
	}
	return filepath.Join(p, nativeID+".sock"), nil
}
