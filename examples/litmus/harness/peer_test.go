package harness

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestReferenceMockPeerPasses(t *testing.T) {
	t.Parallel()

	request := msgPayload()
	response := msgPayload()

	scenario := PeerScenario{
		Name: "happy-path",
		Expectations: []PeerExpectation{{
			Name: "request",
			Check: func(frame []byte) error {
				if !bytes.Equal(frame, request) {
					return errors.New("unexpected request")
				}
				return nil
			},
			Response: [][]byte{response},
		}},
	}

	factory := func(ctx context.Context) (Conn, error) {
		peerSide, clientSide := net.Pipe()
		go func() {
			defer func() { _ = clientSide.Close() }()
			client := NewFramedConn(clientSide)
			if err := client.Send(request); err != nil {
				return
			}
			recvCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _ = client.Recv(recvCtx)
		}()
		return NewFramedConn(peerSide), nil
	}

	peer := &ReferenceMockPeer{Scenarios: []PeerScenario{scenario}, Timeout: time.Second}
	report, err := peer.Verify(context.Background(), factory)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Failures != 0 || report.Counterexample != nil {
		t.Fatalf("report = %+v", report)
	}
	if report.Scenarios != 1 || report.Cases != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestReferenceMockPeerReportsCheckFailure(t *testing.T) {
	t.Parallel()

	request := msgPayload()
	scenario := PeerScenario{
		Name: "bad-request",
		Expectations: []PeerExpectation{{
			Name: "request",
			Check: func([]byte) error {
				return errors.New("invariant INV-01 violated")
			},
		}},
	}

	factory := func(ctx context.Context) (Conn, error) {
		peerSide, clientSide := net.Pipe()
		go func() {
			defer func() { _ = clientSide.Close() }()
			client := NewFramedConn(clientSide)
			if err := client.Send(request); err != nil {
				return
			}
			recvCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, _ = client.Recv(recvCtx)
		}()
		return NewFramedConn(peerSide), nil
	}

	peer := &ReferenceMockPeer{Scenarios: []PeerScenario{scenario}, Timeout: time.Second}
	report, err := peer.Verify(context.Background(), factory)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Failures != 1 || report.Counterexample == nil {
		t.Fatalf("report = %+v", report)
	}
	if report.Counterexample.Violation != "invariant INV-01 violated" {
		t.Fatalf("counterexample = %+v", report.Counterexample)
	}
}

func TestReferenceMockPeerReportsTimeout(t *testing.T) {
	t.Parallel()

	scenario := PeerScenario{
		Name: "timeout",
		Expectations: []PeerExpectation{{
			Name: "request",
		}},
	}

	factory := func(ctx context.Context) (Conn, error) {
		peerSide, clientSide := net.Pipe()
		go func() {
			time.Sleep(200 * time.Millisecond)
			_ = clientSide.Close()
		}()
		return NewFramedConn(peerSide), nil
	}

	peer := &ReferenceMockPeer{Scenarios: []PeerScenario{scenario}, Timeout: 30 * time.Millisecond}
	report, err := peer.Verify(context.Background(), factory)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Failures != 1 || report.Counterexample == nil {
		t.Fatalf("report = %+v", report)
	}
	if report.Counterexample.Violation != "timeout waiting for a client frame" {
		t.Fatalf("counterexample = %+v", report.Counterexample)
	}
}

func TestReferenceMockPeerSendsBeforeFrames(t *testing.T) {
	t.Parallel()

	before := []byte("out-of-order")
	request := msgPayload()

	scenario := PeerScenario{
		Name:   "sequence",
		Before: [][]byte{before},
		Expectations: []PeerExpectation{{
			Name: "request",
			Check: func(frame []byte) error {
				if !bytes.Equal(frame, request) {
					return errors.New("unexpected request")
				}
				return nil
			},
		}},
	}

	factory := func(ctx context.Context) (Conn, error) {
		peerSide, clientSide := net.Pipe()
		go func() {
			defer func() { _ = clientSide.Close() }()
			client := NewFramedConn(clientSide)
			recvCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got, err := client.Recv(recvCtx)
			if err != nil || !bytes.Equal(got, before) {
				return
			}
			_ = client.Send(request)
		}()
		return NewFramedConn(peerSide), nil
	}

	peer := &ReferenceMockPeer{Scenarios: []PeerScenario{scenario}, Timeout: time.Second}
	report, err := peer.Verify(context.Background(), factory)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Failures != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestReferenceMockPeerRequiresScenarios(t *testing.T) {
	t.Parallel()

	peer := &ReferenceMockPeer{}
	if _, err := peer.Verify(context.Background(), func(context.Context) (Conn, error) { return nil, nil }); err == nil {
		t.Fatal("Verify without scenarios returned nil error")
	}
}
