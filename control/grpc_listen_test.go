package control

import (
	"net"
	"testing"
	"time"
)

func TestListenTCPRetryWaitsForPortRelease(t *testing.T) {
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := holder.Addr().String()

	done := make(chan error, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = holder.Close()
	}()
	go func() {
		ln, err := listenTCPRetry(addr, 40, 50*time.Millisecond)
		if err != nil {
			done <- err
			return
		}
		_ = ln.Close()
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("listenTCPRetry: %v", err)
		}
	case <-time.After(3 * time.Second):
		_ = holder.Close()
		t.Fatal("timed out waiting for listen retry")
	}
}

func TestListenTCPRetryGivesUp(t *testing.T) {
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	addr := holder.Addr().String()

	_, err = listenTCPRetry(addr, 2, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected EADDRINUSE after retries")
	}
	if !isAddrInUse(err) {
		t.Fatalf("want addr-in-use, got %v", err)
	}
}

func TestStopGRPCReleasesPortForRelisten(t *testing.T) {
	s := &Service{}
	if err := s.serveGRPC("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	addr := s.grpcLn.Addr().String()
	s.stopGRPC()

	ln, err := listenTCPRetry(addr, 20, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("port still held after stopGRPC: %v", err)
	}
	_ = ln.Close()
}
