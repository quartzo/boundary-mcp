package harness

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestHarnessClientPasses(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	seed := msgPayload()
	response := msgPayload()

	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()

	go echoServer(serverConn, response, -1)

	client := &HarnessClient{Options: ClientOptions{Schema: schema, Timeout: time.Second}}
	report, err := client.Verify(context.Background(), NewFramedConn(clientConn), [][]byte{seed})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Failures != 0 || report.Counterexample != nil {
		t.Fatalf("report = %+v", report)
	}
	if report.Seeds != 1 || report.Mutations == 0 || report.Cases <= 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestHarnessClientReportsClosedConnection(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	seed := msgPayload()

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	// Respond to the seed, then close the connection.
	go echoServer(serverConn, msgPayload(), 1)

	client := &HarnessClient{Options: ClientOptions{Schema: schema, Timeout: time.Second}}
	report, err := client.Verify(context.Background(), NewFramedConn(clientConn), [][]byte{seed})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Failures == 0 || report.Counterexample == nil {
		t.Fatalf("report = %+v, want a failure", report)
	}
}

func TestHarnessClientReportsMissingResponse(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)

	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()

	// Consume the seed without responding.
	go func() {
		conn := NewFramedConn(serverConn)
		_, _ = conn.Recv(context.Background())
	}()

	client := &HarnessClient{Options: ClientOptions{Schema: schema, Timeout: 50 * time.Millisecond}}
	report, err := client.Verify(context.Background(), NewFramedConn(clientConn), [][]byte{msgPayload()})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Counterexample == nil || report.Counterexample.Violation != "no response to a valid payload" {
		t.Fatalf("report = %+v", report)
	}
}

func TestHarnessClientRequiresSeeds(t *testing.T) {
	t.Parallel()

	client := &HarnessClient{}
	if _, err := client.Verify(context.Background(), nil, nil); err == nil {
		t.Fatal("Verify without a connection returned nil error")
	}
}

func echoServer(conn net.Conn, response []byte, limit int) {
	defer func() { _ = conn.Close() }()

	framed := NewFramedConn(conn)
	for i := 0; limit < 0 || i < limit; i++ {
		if _, err := framed.Recv(context.Background()); err != nil {
			return
		}
		if err := framed.Send(response); err != nil {
			return
		}
	}
}
