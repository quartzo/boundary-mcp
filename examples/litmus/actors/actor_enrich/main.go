package main

import (
	"log"
	"net"
	"os"

	flatbuffers "github.com/google/flatbuffers/go"

	"boundary-mcp/examples/litmus/sdk/go/litmus"
)

func main() {
	socket := os.Getenv("BOUNDARY_SOCKET")
	if socket == "" {
		log.Fatal("actor_enrich: BOUNDARY_SOCKET is required")
	}

	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		log.Fatalf("actor_enrich: listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		serve(conn)
		_ = conn.Close()
	}
}

func serve(conn net.Conn) {
	for {
		payload, err := readFrame(conn)
		if err != nil {
			return
		}
		response, ok := process(payload)
		if !ok {
			continue
		}
		if err := writeFrame(conn, response); err != nil {
			return
		}
	}
}

func process(payload []byte) (out []byte, ok bool) {
	defer func() {
		if recover() != nil {
			out = nil
			ok = false
		}
	}()

	envelope := litmus.GetRootAsEnvelope(payload, 0)
	if envelope.Type() != litmus.MessageTypeREQUEST {
		return nil, false
	}

	var request litmus.EnrichRequest
	if envelope.Request(&request) == nil {
		return nil, false
	}

	correlationID := string(request.CorrelationId())
	documentID := string(request.DocumentId())

	if correlationID == "" || !isDocumentID(documentID) {
		return errorEnvelope(correlationID, litmus.ErrorCodeINVALID_DOCUMENT,
			"document_id must contain exactly 14 numeric digits"), true
	}

	return responseEnvelope(correlationID, documentID, 0.92, "Example Corp"), true
}

func isDocumentID(value string) bool {
	if len(value) != 14 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func responseEnvelope(correlationID, documentID string, confidence float32, displayName string) []byte {
	builder := flatbuffers.NewBuilder(256)
	correlation := builder.CreateString(correlationID)
	document := builder.CreateString(documentID)
	display := builder.CreateString(displayName)

	litmus.EnrichResponseStart(builder)
	litmus.EnrichResponseAddCorrelationId(builder, correlation)
	litmus.EnrichResponseAddDocumentId(builder, document)
	litmus.EnrichResponseAddConfidenceScore(builder, confidence)
	litmus.EnrichResponseAddDisplayName(builder, display)
	response := litmus.EnrichResponseEnd(builder)

	litmus.EnvelopeStart(builder)
	litmus.EnvelopeAddType(builder, litmus.MessageTypeRESPONSE)
	litmus.EnvelopeAddResponse(builder, response)
	envelope := litmus.EnvelopeEnd(builder)
	builder.Finish(envelope)

	return append([]byte(nil), builder.FinishedBytes()...)
}

func errorEnvelope(correlationID string, code litmus.ErrorCode, message string) []byte {
	builder := flatbuffers.NewBuilder(256)
	correlation := builder.CreateString(correlationID)
	messageOffset := builder.CreateString(message)

	litmus.EnrichErrorStart(builder)
	litmus.EnrichErrorAddCorrelationId(builder, correlation)
	litmus.EnrichErrorAddCode(builder, code)
	litmus.EnrichErrorAddMessage(builder, messageOffset)
	errorOffset := litmus.EnrichErrorEnd(builder)

	litmus.EnvelopeStart(builder)
	litmus.EnvelopeAddType(builder, litmus.MessageTypeERROR)
	litmus.EnvelopeAddError(builder, errorOffset)
	envelope := litmus.EnvelopeEnd(builder)
	builder.Finish(envelope)

	return append([]byte(nil), builder.FinishedBytes()...)
}
