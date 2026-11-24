package main

import (
	"log"
	"net"
	"os"
	"time"

	flatbuffers "github.com/google/flatbuffers/go"

	"boundary-mcp/examples/litmus/sdk/go/litmus"
)

const (
	requestTimeout = 500 * time.Millisecond
	maxRetries     = 2
	correlationID  = "0192f0a0-0000-7000-8000-000000000001"
	documentID     = "12345678901234"
)

func main() {
	socket := os.Getenv("BOUNDARY_SOCKET")
	if socket == "" {
		log.Fatal("actor_ingest: BOUNDARY_SOCKET is required")
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		log.Fatalf("actor_ingest: dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	payload := requestEnvelope(correlationID, documentID)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := conn.SetDeadline(time.Now().Add(requestTimeout)); err != nil {
			log.Fatalf("actor_ingest: deadline: %v", err)
		}
		if err := writeFrame(conn, payload); err != nil {
			log.Fatalf("actor_ingest: send: %v", err)
		}

		response, err := readFrame(conn)
		if err == nil {
			if _, ok := parseResponse(response); ok {
				return
			}
		}

		if attempt < maxRetries {
			backoff := time.Duration(50*(1<<attempt)) * time.Millisecond
			time.Sleep(backoff)
		}
	}

	log.Fatal("actor_ingest: dead-letter after retries")
}

func requestEnvelope(correlationID, documentID string) []byte {
	builder := flatbuffers.NewBuilder(256)
	correlation := builder.CreateString(correlationID)
	document := builder.CreateString(documentID)

	litmus.EnrichRequestStart(builder)
	litmus.EnrichRequestAddCorrelationId(builder, correlation)
	litmus.EnrichRequestAddDocumentId(builder, document)
	request := litmus.EnrichRequestEnd(builder)

	litmus.EnvelopeStart(builder)
	litmus.EnvelopeAddType(builder, litmus.MessageTypeREQUEST)
	litmus.EnvelopeAddRequest(builder, request)
	envelope := litmus.EnvelopeEnd(builder)
	builder.Finish(envelope)

	return append([]byte(nil), builder.FinishedBytes()...)
}

func parseResponse(payload []byte) (envelope *litmus.Envelope, ok bool) {
	defer func() {
		if recover() != nil {
			envelope = nil
			ok = false
		}
	}()

	envelope = litmus.GetRootAsEnvelope(payload, 0)
	if envelope.Type() == litmus.MessageTypeRESPONSE || envelope.Type() == litmus.MessageTypeERROR {
		return envelope, true
	}
	return nil, false
}
