# Reconstruct actor_enrich

You are working inside an isolated actor workspace. You cannot read the source
code of other actors, and the governance directory is not mounted.

Use the `boundary` MCP server to obtain:

- the filtered `purpose.md` for `actor_enrich`;
- the full `protocol.fbs`;
- the read-only SDK path.

Implement `actor_enrich` in Go in the current workspace.

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

Do not implement `actor_ingest`. Do not change the contract. If the contract is
insufficient, propose an evolution through the boundary MCP server.
