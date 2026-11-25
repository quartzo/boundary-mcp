package harness

import (
	"context"
	"errors"
	"net"
	"os"
	"time"
)

// Conn is a framed bidirectional message channel.
type Conn interface {
	Send(payload []byte) error
	Recv(ctx context.Context) ([]byte, error)
	Close() error
}

// FramedConn adapts a net.Conn to the harness framing
// ([uint32 LE length][payload]).
type FramedConn struct {
	conn net.Conn
	r    *Reader
	w    *Writer
}

// NewFramedConn wraps conn.
func NewFramedConn(conn net.Conn) *FramedConn {
	return &FramedConn{
		conn: conn,
		r:    NewReader(conn, 0),
		w:    NewWriter(conn),
	}
}

// Send writes one framed payload.
func (c *FramedConn) Send(payload []byte) error {
	return c.w.WriteFrame(payload)
}

// Recv reads one framed payload, bounded by the context deadline.
func (c *FramedConn) Recv(ctx context.Context) ([]byte, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
	} else {
		_ = c.conn.SetReadDeadline(time.Time{})
	}

	payload, err := c.r.ReadFrame()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, context.DeadlineExceeded
		}
		return nil, err
	}
	return payload, nil
}

// Close closes the underlying connection.
func (c *FramedConn) Close() error { return c.conn.Close() }

// DialUnix connects to a Unix domain socket.
func DialUnix(ctx context.Context, path string) (Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	return NewFramedConn(conn), nil
}

// ListenUnix listens on a Unix domain socket, removing a stale socket file.
func ListenUnix(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return net.Listen("unix", path)
}

// Accept accepts a connection, bounded by ctx.
func Accept(ctx context.Context, listener net.Listener) (Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan result, 1)
	go func() {
		conn, err := listener.Accept()
		accepted <- result{conn: conn, err: err}
	}()

	select {
	case <-ctx.Done():
		_ = listener.Close()
		return nil, ctx.Err()
	case res := <-accepted:
		if res.err != nil {
			return nil, res.err
		}
		return NewFramedConn(res.conn), nil
	}
}
