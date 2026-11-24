// actor_enrich is the receiver/server actor of the litmus entity
// enrichment pipeline.
//
// It listens on the Unix socket named by BOUNDARY_SOCKET and speaks the
// boundary framing: a 4-byte little-endian length followed by that many
// bytes of a FlatBuffers Envelope.
//
// For Envelope.type == REQUEST it validates the EnrichRequest payload:
//   - correlation_id must be non-empty;
//   - document_id must contain exactly 14 numeric digits (INV-02).
//
// Invalid input is answered with an Envelope carrying an EnrichError with
// ErrorCode.INVALID_DOCUMENT. Valid input is answered with an Envelope
// carrying an EnrichResponse whose confidence_score is deterministic for a
// given document_id (INV-04: requests sharing a correlation_id must yield
// byte-identical response payloads).
//
// Malformed frames never panic the process and never close the
// connection: they are answered with a typed EnrichError (or dropped when
// no payload was readable at all).
package main

import (
	"encoding/binary"
	"hash/fnv"
	"io"
	"log"
	"net"
	"os"
	"regexp"

	flatbuffers "github.com/google/flatbuffers/go"
	"litmus"
)

// maxFrameSize bounds how large a single frame body may be. Larger declared
// lengths are treated as corrupted framing.
const maxFrameSize = 1 << 20 // 1 MiB

// docIDPattern matches document IDs made of exactly 14 numeric digits.
var docIDPattern = regexp.MustCompile(`^[0-9]{14}$`)

func main() {
	socketPath := os.Getenv("BOUNDARY_SOCKET")
	if socketPath == "" {
		log.Fatal("actor_enrich: BOUNDARY_SOCKET is not set")
	}
	// Remove a stale socket file left by a previous run, if any.
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("actor_enrich: listening on %s: %v", socketPath, err)
	}
	log.Printf("actor_enrich: listening on %s", socketPath)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("actor_enrich: accept: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

// handleConnection reads frames off conn until the peer goes away. A single
// bad frame never tears the connection down.
func handleConnection(conn net.Conn) {
	defer conn.Close()

	var lenBuf [4]byte
	for {
		if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
			return // peer closed the connection or I/O error
		}
		n := binary.LittleEndian.Uint32(lenBuf[:])

		if n == 0 {
			continue // nothing to process; drop the empty frame
		}
		if n > maxFrameSize {
			// The declared length is untrustworthy; consuming the body
			// could block or exhaust memory. Reply with a typed error
			// and keep the connection open without desynchronizing on
			// data we refuse to trust.
			log.Printf("actor_enrich: dropping frame claiming %d bytes", n)
			writeFrame(conn, errorEnvelope("", "frame length exceeds limit"))
			continue
		}

		payload := make([]byte, n)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return // truncated frame: the peer is gone
		}

		if resp := processFrame(payload); resp != nil {
			if err := writeFrame(conn, resp); err != nil {
				return
			}
		}
	}
}

// processFrame decodes one Envelope and returns the reply frame to send
// back, or nil when the frame should be dropped silently. Any panic raised
// while interpreting untrusted bytes is recovered and turned into a typed
// INVALID_DOCUMENT error so that malformed input can never crash the actor.
func processFrame(payload []byte) (resp []byte) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("actor_enrich: recovered from malformed frame: %v", r)
			resp = errorEnvelope("", "malformed frame")
		}
	}()

	if len(payload) < 4 {
		return errorEnvelope("", "malformed frame: too short")
	}

	env := litmus.GetRootAsEnvelope(payload, 0)
	if env.Type() != litmus.MessageTypeREQUEST {
		// Only REQUEST envelopes are actionable for this actor;
		// anything else is dropped silently.
		return nil
	}

	req := env.Request(new(litmus.EnrichRequest))
	if req == nil {
		return errorEnvelope("", "missing EnrichRequest")
	}

	correlationID := string(req.CorrelationId())
	documentID := string(req.DocumentId())

	if correlationID == "" {
		return errorEnvelope(correlationID, "correlation_id must be non-empty")
	}
	if !docIDPattern.MatchString(documentID) {
		return errorEnvelope(correlationID, "document_id must contain exactly 14 numeric digits")
	}

	return successEnvelope(correlationID, documentID)
}

// confidenceFor derives a deterministic confidence score in [0.0, 1.0) from
// the document ID, so that repeated requests with the same correlation_id
// produce byte-identical responses (INV-04).
func confidenceFor(documentID string) float32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(documentID))
	return float32(h.Sum32()%1000) / 1000.0
}

// successEnvelope builds an Envelope{type: RESPONSE, response: EnrichResponse}.
func successEnvelope(correlationID, documentID string) []byte {
	b := flatbuffers.NewBuilder(256)

	displayName := b.CreateString("doc-" + documentID)
	correlation := b.CreateString(correlationID)
	document := b.CreateString(documentID)

	litmus.EnrichResponseStart(b)
	litmus.EnrichResponseAddCorrelationId(b, correlation)
	litmus.EnrichResponseAddDocumentId(b, document)
	litmus.EnrichResponseAddConfidenceScore(b, confidenceFor(documentID))
	litmus.EnrichResponseAddDisplayName(b, displayName)
	response := litmus.EnrichResponseEnd(b)

	litmus.EnvelopeStart(b)
	litmus.EnvelopeAddType(b, litmus.MessageTypeRESPONSE)
	litmus.EnvelopeAddResponse(b, response)
	envelope := litmus.EnvelopeEnd(b)

	b.Finish(envelope)
	return b.FinishedBytes()
}

// errorEnvelope builds an Envelope{type: ERROR, error: EnrichError} carrying
// ErrorCode.INVALID_DOCUMENT.
func errorEnvelope(correlationID, message string) []byte {
	b := flatbuffers.NewBuilder(256)

	correlation := b.CreateString(correlationID)
	msg := b.CreateString(message)

	litmus.EnrichErrorStart(b)
	litmus.EnrichErrorAddCorrelationId(b, correlation)
	litmus.EnrichErrorAddCode(b, litmus.ErrorCodeINVALID_DOCUMENT)
	litmus.EnrichErrorAddMessage(b, msg)
	errOff := litmus.EnrichErrorEnd(b)

	litmus.EnvelopeStart(b)
	litmus.EnvelopeAddType(b, litmus.MessageTypeERROR)
	litmus.EnvelopeAddError(b, errOff)
	envelope := litmus.EnvelopeEnd(b)

	b.Finish(envelope)
	return b.FinishedBytes()
}

// writeFrame writes one length-prefixed frame: 4-byte little-endian length
// followed by the raw FlatBuffer bytes.
func writeFrame(conn net.Conn, frame []byte) error {
	out := make([]byte, 4+len(frame))
	binary.LittleEndian.PutUint32(out[:4], uint32(len(frame)))
	copy(out[4:], frame)
	_, err := conn.Write(out)
	return err
}
