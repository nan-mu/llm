package unix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
)

// dialJSON opens one UDS connection, writes req JSON, half-closes write, reads one JSON response.
func dialJSON(ctx context.Context, socket string, body []byte) ([]byte, error) {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		return nil, fmt.Errorf("no socket")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if len(body) == 0 {
		body = []byte("{}")
	}
	if _, err := conn.Write(body); err != nil {
		return nil, err
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(resp))) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	if !json.Valid(resp) {
		return nil, fmt.Errorf("invalid json response")
	}
	return resp, nil
}
