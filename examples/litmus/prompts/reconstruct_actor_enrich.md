# Reconstruct actor_enrich

You are working inside an isolated actor workspace. You cannot read the source
code of other actors, and the governance directory is not mounted.

## Boundary context

Use the `boundary` MCP server:

- `get_boundary_context` (exposed as `boundary_get_boundary_context`) with
  `actor_name=actor_enrich` to obtain the filtered `purpose.md`, the full
  `protocol.fbs` and the read-only SDK path.
- `verify_actor_behavior` (exposed as `boundary_verify_actor_behavior`) with
  `actor_name=actor_enrich` and `entrypoint_cmd=go build -o bin/actor .` to run
  the hidden suite.

Do not change the contract. If the contract is insufficient, propose an
evolution through `boundary_propose_contract_evolution` instead of editing the
schema.

## Workspace setup

The generated SDK module is mounted read-only at `/sdk/go/litmus` with module
path `litmus`. Create a `go.mod` in `/workspace`:

```text
module actor_enrich

go 1.27.1

require (
    github.com/google/flatbuffers v25.12.19+incompatible
    litmus v0.0.0
)

replace litmus => ../../sdk/go/litmus
```

Import the generated bindings as `"litmus"`. Run `go mod tidy` if needed, then
`go build -o bin/actor .`.

## Requirements

- Listen on the Unix socket from `BOUNDARY_SOCKET`.
- Speak the length-prefixed framing: 4-byte little-endian length + FlatBuffer.
- Decode `Envelope` from the generated SDK.
- For `MessageType.REQUEST`, validate `EnrichRequest`:
  - `correlation_id` must be non-empty;
  - `document_id` must contain exactly 14 numeric digits.
- On invalid input, reply with an `Envelope` containing `EnrichError` and
  `ErrorCode.INVALID_DOCUMENT`.
- On success, reply with an `Envelope` containing `EnrichResponse` with
  `confidence_score` in `[0.0, 1.0]`.
- Never panic on malformed frames. Drop them or return a typed error without
  closing the connection.
- Do not read or modify any other actor.

Do not implement `actor_ingest`.
