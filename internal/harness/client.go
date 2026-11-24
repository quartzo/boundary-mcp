package harness

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ClientOptions configures the harness that verifies an actor as a server.
type ClientOptions struct {
	// Schema validates responses. Optional.
	Schema *Schema
	// ResponseTable is the table used to validate responses. Empty means the
	// schema root table.
	ResponseTable string
	// Timeout bounds each request/response exchange.
	Timeout time.Duration
	// MaxCases caps the number of executed cases. Zero means unlimited.
	MaxCases int
}

// HarnessClient verifies actors that receive messages: it sends valid seed
// frames and structural mutations and checks the responses.
type HarnessClient struct {
	Options ClientOptions
}

// ClientCounterexample is the smallest failing case observed.
type ClientCounterexample struct {
	Case      string `json:"case"`
	Payload   []byte `json:"payload"`
	Observed  string `json:"observed"`
	Violation string `json:"violation"`
}

// ClientReport summarizes a server-side verification run.
type ClientReport struct {
	Seeds          int                   `json:"seeds"`
	Mutations      int                   `json:"mutations"`
	Cases          int                   `json:"cases"`
	Failures       int                   `json:"failures"`
	P99LatencyUS   int64                 `json:"p99_latency_us"`
	Counterexample *ClientCounterexample `json:"counterexample,omitempty"`
}

// Verify exercises an actor through conn.
//
// A valid seed must produce a response. A structural mutation may be dropped
// cleanly (timeout), but it must not close the connection or produce an
// invalid FlatBuffer.
func (h *HarnessClient) Verify(ctx context.Context, conn Conn, seeds [][]byte) (*ClientReport, error) {
	if conn == nil {
		return nil, errors.New("harness: nil connection")
	}
	if len(seeds) == 0 {
		return nil, errors.New("harness: at least one seed payload is required")
	}

	timeout := h.Options.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	report := &ClientReport{}
	var latencies []time.Duration
	failed := false

	fail := func(name string, payload []byte, observed, violation string) bool {
		report.Failures++
		if report.Counterexample == nil {
			report.Counterexample = &ClientCounterexample{
				Case:      name,
				Payload:   append([]byte(nil), payload...),
				Observed:  observed,
				Violation: violation,
			}
		}
		failed = true
		return false
	}

	run := func(name string, payload []byte, expectResponse, strict bool) bool {
		if h.Options.MaxCases > 0 && report.Cases >= h.Options.MaxCases {
			return false
		}
		report.Cases++
		start := time.Now()

		if err := conn.Send(payload); err != nil {
			return fail(name, payload, "send error", err.Error())
		}

		recvCtx, cancel := context.WithTimeout(ctx, timeout)
		response, err := conn.Recv(recvCtx)
		cancel()
		elapsed := time.Since(start)

		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if expectResponse {
					return fail(name, payload, "timeout", "no response to a valid payload")
				}
				return true
			}
			return fail(name, payload, "connection error", err.Error())
		}

		latencies = append(latencies, elapsed)
		if h.Options.Schema != nil {
			var verifyErr error
			if strict {
				verifyErr = h.Options.Schema.ValidateFor(response, h.Options.ResponseTable)
			} else {
				verifyErr = ValidateBuffer(response)
			}
			if verifyErr != nil {
				return fail(name, payload, "invalid response "+preview(response), verifyErr.Error())
			}
		}
		return true
	}

	for i, seed := range seeds {
		report.Seeds++
		if !run(fmt.Sprintf("seed[%d]", i), seed, true, true) {
			break
		}
	}

	if !failed && h.Options.Schema != nil && (h.Options.MaxCases <= 0 || report.Cases < h.Options.MaxCases) {
		mutations, err := h.Options.Schema.BoundaryMutations(seeds[0])
		if err != nil {
			return nil, fmt.Errorf("harness: generate mutations: %w", err)
		}
		for _, mutation := range mutations {
			report.Mutations++
			if !run(mutation.Name, mutation.Payload, false, false) {
				break
			}
		}
	}

	report.P99LatencyUS = percentileUS(latencies, 0.99)
	return report, nil
}

func percentileUS(values []time.Duration, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := int(float64(len(values)-1) * p)
	return values[index].Microseconds()
}

func preview(payload []byte) string {
	const max = 64
	if len(payload) > max {
		return hex.EncodeToString(payload[:max]) + "..."
	}
	return hex.EncodeToString(payload)
}
