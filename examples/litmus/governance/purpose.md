# System: Entity Enrichment Pipeline

The litmus example exercises a two-actor boundary: one emitter and one receiver.
Its purpose is to validate isolation, contract enforcement and disposability,
not to be a production pipeline.

## Actors

1. **actor_ingest** (Emitter / Client): Sends enrichment requests to the receiver.
2. **actor_enrich** (Receiver / Server): Validates requests and returns enrichment metadata.

## Protocol & State Flow

- **Initial State:** `IDLE`
- **Transition 1:** `actor_ingest` sends an `Envelope` with `type=REQUEST` and an `EnrichRequest` -> `PENDING`.
- **Transition 2:** `actor_enrich` replies with an `Envelope` containing `EnrichResponse` (success) or `EnrichError` (failure) -> `COMPLETED` or `FAILED`.
- **Transition 3 (retry):** if `actor_enrich` does not answer within 500ms, `actor_ingest` retries on the same connection -> `PENDING`.
- **Transition 4 (dead letter):** after two retries, `actor_ingest` gives up -> `DEAD_LETTER`.

## Boundary Invariants (Hard Contracts)

- **INV-01:** `EnrichRequest.correlation_id` must be a valid non-empty UUIDv7.
- **INV-02:** `EnrichRequest.document_id` must contain exactly 14 numeric digits. Otherwise, `actor_enrich` MUST return `EnrichError.code = INVALID_DOCUMENT`.
- **INV-03:** `EnrichResponse.confidence_score` must fall strictly within `[0.0, 1.0]`.
- **INV-04 (Idempotency):** Two requests sharing the same `correlation_id` must yield byte-identical response payloads.
- **INV-05 (Client Resilience):** If `actor_enrich` does not respond within 500ms, `actor_ingest` must retry at most twice with exponential backoff before dead-lettering.
