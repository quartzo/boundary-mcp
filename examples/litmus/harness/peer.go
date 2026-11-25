package harness

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ConnFactory opens a fresh connection for one scenario.
type ConnFactory func(ctx context.Context) (Conn, error)

// PeerCheck validates a frame received from the actor under test.
type PeerCheck func(request []byte) error

// PeerExpectation describes one request/response step.
type PeerExpectation struct {
	// Name identifies the step in reports.
	Name string
	// Check inspects the request before responding.
	Check PeerCheck
	// Delay waits before sending Response (used for timeout scenarios).
	Delay time.Duration
	// Response frames are sent after Check (and Delay). Nil means no response.
	Response [][]byte
}

// PeerScenario is a scripted interaction with a client actor.
type PeerScenario struct {
	Name         string
	Before       [][]byte
	Expectations []PeerExpectation
	// Timeout overrides ReferenceMockPeer.Timeout for this scenario.
	Timeout time.Duration
}

// ReferenceMockPeer verifies actors that emit messages: it acts as the remote
// peer, executes scripted scenarios and returns a verdict. It is the test
// oracle for client actors.
type ReferenceMockPeer struct {
	Scenarios []PeerScenario
	Timeout   time.Duration
}

// PeerCounterexample is the first failing step.
type PeerCounterexample struct {
	Scenario  string `json:"scenario"`
	Step      string `json:"step"`
	Observed  []byte `json:"observed,omitempty"`
	Violation string `json:"violation"`
}

// PeerReport summarizes a client-side verification run.
type PeerReport struct {
	Scenarios      int                 `json:"scenarios"`
	Cases          int                 `json:"cases"`
	Failures       int                 `json:"failures"`
	Counterexample *PeerCounterexample `json:"counterexample,omitempty"`
}

// Verify runs all scenarios, each on a fresh connection from factory.
func (p *ReferenceMockPeer) Verify(ctx context.Context, factory ConnFactory) (*PeerReport, error) {
	if factory == nil {
		return nil, errors.New("harness: nil connection factory")
	}
	if len(p.Scenarios) == 0 {
		return nil, errors.New("harness: at least one scenario is required")
	}

	defaultTimeout := p.Timeout
	if defaultTimeout <= 0 {
		defaultTimeout = 2 * time.Second
	}

	report := &PeerReport{}
	for _, scenario := range p.Scenarios {
		report.Scenarios++
		if err := p.runScenario(ctx, factory, scenario, defaultTimeout, report); err != nil {
			return nil, err
		}
	}
	return report, nil
}

func (p *ReferenceMockPeer) runScenario(ctx context.Context, factory ConnFactory, scenario PeerScenario, defaultTimeout time.Duration, report *PeerReport) error {
	conn, err := factory(ctx)
	if err != nil {
		return fmt.Errorf("harness: scenario %s: %w", scenario.Name, err)
	}
	defer func() { _ = conn.Close() }()

	timeout := scenario.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	failed := false
	fail := func(step string, observed []byte, violation string) {
		report.Failures++
		if report.Counterexample == nil {
			report.Counterexample = &PeerCounterexample{
				Scenario:  scenario.Name,
				Step:      step,
				Observed:  append([]byte(nil), observed...),
				Violation: violation,
			}
		}
		failed = true
	}

	for _, frame := range scenario.Before {
		if err := conn.Send(frame); err != nil {
			fail("before", nil, "send: "+err.Error())
			return nil
		}
	}

	for i, expectation := range scenario.Expectations {
		if failed {
			break
		}
		report.Cases++

		step := expectation.Name
		if step == "" {
			step = fmt.Sprintf("step[%d]", i)
		}

		recvCtx, cancel := context.WithTimeout(ctx, timeout)
		request, err := conn.Recv(recvCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				fail(step, nil, "timeout waiting for a client frame")
			} else {
				fail(step, nil, err.Error())
			}
			break
		}

		if expectation.Check != nil {
			if err := expectation.Check(request); err != nil {
				fail(step, request, err.Error())
				break
			}
		}

		if expectation.Delay > 0 {
			timer := time.NewTimer(expectation.Delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}

		for _, response := range expectation.Response {
			if err := conn.Send(response); err != nil {
				fail(step, request, "send: "+err.Error())
				break
			}
		}
	}

	return nil
}
