package harness

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestFramedConnRoundTrip(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	go func() {
		conn := NewFramedConn(server)
		payload, err := conn.Recv(context.Background())
		if err != nil {
			return
		}
		_ = conn.Send(payload)
	}()

	conn := NewFramedConn(client)
	if err := conn.Send([]byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := conn.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("payload = %q", got)
	}
}

func TestFramedConnRecvTimeout(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	conn := NewFramedConn(client)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := conn.Recv(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestDialUnix(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "harness.sock")
	listener, err := ListenUnix(path)
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer func() { _ = listener.Close() }()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		peer := NewFramedConn(conn)
		payload, err := peer.Recv(context.Background())
		if err != nil {
			_ = conn.Close()
			return
		}
		_ = peer.Send(payload)
		_ = conn.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := DialUnix(ctx, path)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.Send([]byte("ping")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := conn.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("payload = %q", got)
	}
}
