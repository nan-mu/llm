package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// PingJSON dials a JSON-over-UDS worker with {"op":"health"} and expects {"ok":true}.
func PingJSON(ctx context.Context, socket string) error {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		return fmt.Errorf("no socket")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	}
	if _, err := conn.Write([]byte(`{"op":"health"}`)); err != nil {
		return err
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	raw, err := io.ReadAll(conn)
	if err != nil {
		return err
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("health not ok")
	}
	return nil
}
