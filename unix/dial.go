package unix

import (
	"context"
	"io"
	"net"
)

// dialJSON writes one JSON request and reads one JSON response on a Unix socket.
func dialJSON(ctx context.Context, socket string, body []byte) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write(body); err != nil {
		return nil, err
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	return io.ReadAll(conn)
}
