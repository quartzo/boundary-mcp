# Plan: Protocol-Only boundary-mcp

- **Status:** Stages 1–2 implemented; Stages 3–7 pending
- **Date:** 2026-11-25
- **Decision record:** [ADR-0004](adr/0004-protocol-only-mcp.md)
- **Supersedes in scope:** the actor-runtime and actor-verification parts of [ARCHITECTURE.md](ARCHITECTURE.md) §6–§7 and [WORKFLOWS.md](WORKFLOWS.md) §2–§3

## 1. Why this plan exists

The repository currently carries three responsibilities:

1. **Contract management** — `purpose.md` ↔ `protocol.fbs` ↔ `state_machine.json`, `flatc` gates, the semantic engine, versions and compatibility.
2. **Agent runtime** — `cmd/sandbox-run` launches a pinned `opencode` inside `bwrap`.
3. **Actor behavior verification** — `verify_actor_behavior` builds and runs the actor binary against hidden suites.

Only (1) is the product. (2) and (3) were introduced to make the Phase 5 disposability experiment reproducible, and were then documented as the product's isolation and verification models. That inversion has concrete costs:

- the coder of the user became a dependency of the contract manager;
- `verify_actor_behavior` executes code the server did not write (`internal/harness/verifier.go:103`), and accepts an agent-supplied `entrypoint_cmd` (`:75-80`);
- the MCP assumes it shares a filesystem with the actor's source (`actors/<name>`), although the actor code never travels through the protocol;
- "capability-based physical isolation" is a property the server cannot provide for an external client;
- hidden suites exist to prevent overfitting to *behavioral* tests, which only matters while the server runs those tests.

## 2. Target architecture

```text
  user's coder (opencode or any MCP client)
    owns the actor directory for one service
          |
          |  MCP: context, proposals, protocol test specs
          |  (no actor code, no actor execution)
          v
  boundary-mcp (system)
    governance · compiler · flatc · semantic engine · versioning · protocol tests
          |
          +--> resource: boundary://protocol/<hash>/fbs
          +--> resource: boundary://protocol/<hash>/tests
          +--> resource: boundary://protocol/<hash>/manifest
          |
  user's CI
    runs the normative spec against the actor directory and gates the merge
```

Ownership:

| Concern | Owner | Server responsibility |
| --- | --- | --- |
| Actor source | user's coder | none — never read or received |
| Actor isolation | user's workspace layout + `external_directory: deny` | publish the recommended rule |
| Protocol meaning | boundary-mcp (semantic engine) | decide and record |
| Protocol conformance | boundary-mcp (`flatc`, diff) | decide and record |
| Protocol tests | boundary-mcp (spec) | publish, version, keep deterministic |
| Running the tests | user's coder (iteration) and CI (gate) | none |
| Authority to merge | user's CI | none |

## 3. Interface

### Resources (immutable, content-addressed)

| URI | Content |
| --- | --- |
| `boundary://protocol/{hash}/fbs` | `protocol.fbs` and, when present, the compiled `.bfbs` |
| `boundary://protocol/{hash}/purpose` | the full `purpose.md` |
| `boundary://protocol/{hash}/tests` | the normative protocol test specs |
| `boundary://protocol/{hash}/manifest` | compatibility manifest (deferred: it needs a base version, which is a tool argument) |
| `boundary://actor/{name}/context` | filtered purpose + active protocol + SDK path |

`mcp-go v1.6.0` supports `AddResource` and `AddResourceTemplate`; the server currently enables tool capabilities only (`WithToolCapabilities(false)`), so resource capabilities must be enabled.

### Tools (actions only)

| Tool | Status |
| --- | --- |
| `get_boundary_context` | keep |
| `propose_contract_evolution` | keep |
| `get_protocol_tests` | add as fallback for clients that do not read resources |
| `verify_actor_behavior` | **remove** |

## 4. Protocol test spec

A declarative, public, content-addressed specification. It never references an actor binary, a build command or a process.

```json
{
  "spec_version": 1,
  "protocol": "sha256:...",
  "schema": "protocol.bfbs",
  "seeds": [
    { "name": "request", "table": "Envelope", "values": { "type": "REQUEST", "request": { "document_id": "12345678901234" } } }
  ],
  "rules": [
    {
      "table": "EnrichRequest",
      "field": "document_id",
      "regex": "^[0-9]{14}$",
      "valid":   ["12345678901234"],
      "invalid": ["", "1234", "123456789012345", "abcdefghijklmn"]
    },
    {
      "table": "EnrichResponse",
      "field": "confidence_score",
      "min": 0,
      "max": 1,
      "valid":   [0, 0.92, 1],
      "invalid": [-0.1, 1.01]
    }
  ],
  "mutations": ["truncate", "bad-offset", "invalid-utf8", "flip"],
  "roundtrip": true,
  "generation": { "cases": 12 }
}
```

Rules carry explicit `valid`/`invalid` examples so expansion never has to invent values from a regex or a range. `regex`, `format`, `min` and `max` are normative, and expansion rejects a spec whose examples contradict them. `seeds` provide the valid frames that mutations derive from. `generation.cases`, when set, is a consistency check: expansion fails if it produces a different count.

Requirements:

- **Deterministic cases.** Either the spec carries the generated cases or it carries the generator + seed, so two runners produce the same set.
- **Closed semantics.** "Reject" must be defined (drop the frame, or return a typed error; within what bound). Ambiguity makes the spec unenforceable.
- **No execution keys.** `mode`, `build`, `run`, `timeout_ms`, `scenarios` and `entrypoint_cmd` do not exist in this format.
- **Public by default.** The spec is normative and shared; hiding it only protected behavioral tests, which leave the system.

The generator reuses the existing, actor-free pieces: `.bfbs` reflection (`internal/harness/schema.go`), frame building (`build.go`) and structural mutations (`payload.go`).

## 5. What moves, what is deleted

| Path | Action |
| --- | --- |
| `internal/harness/verifier.go` | move to `examples/litmus/` (Stage 3), then delete from `internal` |
| `internal/harness/client.go`, `peer.go`, `suite.go`, `checks.go`, `wire.go` | move to `examples/litmus/` (behavioral runners) |
| `internal/harness/schema.go`, `fb.go`, `payload.go`, `build.go` | keep as the protocol-test core (package rename optional) |
| `internal/fbs/framing.go` | move to the example (data-plane transport) |
| `examples/litmus/governance/protocol_tests/` | add now; `hidden_tests/` stays until Stage 3 |
| `examples/litmus/governance/hidden_tests/` | delete with the behavioral engine (Stage 3) |
| `examples/litmus/verify/` | repurpose as the reference runner used by `make litmus` |
| `cmd/sandbox-run/` | move to `examples/litmus/sandbox-run/` |
| `mcp.Options.Verifier`, `-actors`, `-hidden-tests` | delete |
| `verify_actor_behavior` tool and `BehaviorVerifier` | delete |

## 6. Staged work

### Stage 1 — Protocol test spec and resources (implemented)

- `internal/protocoltest`: spec type, strict parser, validator and deterministic expansion.
- The generator reuses `.bfbs` reflection, the frame builder and the structural mutations.
- Protocol resources (`fbs`, `purpose`, `tests`) and the actor context template are registered together with resource capabilities.
- `get_protocol_tests` is available as a fallback tool.
- `examples/litmus/governance/protocol_tests/protocol.json` sits next to `hidden_tests/`; the behavioral suites are removed in Stage 3, not here.

**Acceptance:** `boundary://protocol/{hash}/tests` returns the validated specs; repeated `protocoltest.Expand` of the same spec is byte-identical; no spec key implies a process.

### Stage 2 — Remove the execution path (implemented)

- `verify_actor_behavior`, `BehaviorVerifier`, the verifier wiring and the `-actors`/`-hidden-tests` flags are gone.
- `harness` no longer depends on `mcp`; the reference verifier is used directly by `examples/litmus/verify`.
- No `exec.Command` remains under `internal/mcp`, `internal/compiler` or `internal/versioning` except `flatc`.

**Acceptance:** the server builds and serves its tools and resources with no actor directory present; no tool runs a subprocess.

### Stage 3 — Split the harness

- Isolate the protocol-test core from the behavioral engine.
- Move the behavioral engine and suites under `examples/litmus` and update their tests.

**Acceptance:** `go build ./...` and `go test ./...` pass; the litmus example still runs end to end.

### Stage 4 — Move the agent runtime out

- `git mv cmd/sandbox-run examples/litmus/sandbox-run`.
- Remove it from the README quick start and the Makefile product targets.
- Document connecting an existing coder: `opencode.json` → `mcp.servers`, bearer token or OAuth, and the recommended `external_directory: deny` rule.

**Acceptance:** no product path mentions `bwrap`; the only reference is the example and this plan.

### Stage 5 — Rewrite the docs

- README: status, quick start, and the "capability isolation" claim.
- ARCHITECTURE: replace §6–§7 with protocol testing and the external-coder model.
- WORKFLOWS: replace the actor-verification workflow with "coder iterates on the spec; CI gates".
- ADR index and cross-links.

**Acceptance:** `rg 'verify_actor_behavior|sandbox-run|physical isolation'` matches only historical ADRs, this plan, and `examples/`.

### Stage 6 — Hardening that remains

- Scope the bearer token to one actor when HTTP is exposed beyond loopback.
- Validate `actor_name` against the actors declared in `purpose.md`.
- Run `flatc` with a timeout and resource limits (`.fbs` is untrusted input to a C++ parser).
- Never return another actor's context or `governance/` paths in a tool response.

**Acceptance:** a token for `actor_ingest` cannot read or propose for `actor_enrich`.

### Stage 7 — Make the gate explicit

- Document that merge authority lives in the user's CI, not in the server.
- Provide a reference runner target (`make protocol-test`) in the example that consumes the spec and exits non-zero on drift.

**Acceptance:** the reference runner reproduces the spec's cases byte-for-byte and is the only thing described as a gate.

## 7. Non-goals

- Running actor code, building actor binaries, or reading actor directories.
- Managing or launching the coder.
- Enforcing merges server-side.
- Hidden tests, overfitting prevention, or behavioral pass/fail.
- Being CI.

## 8. Risks

- **Breaking interface.** Removing `verify_actor_behavior` is a breaking MCP change; bump the server version and document the migration in `docs/WORKFLOWS.md`.
- **Losing the disposability evidence.** The Phase 5 litmus stays as an example and keeps its result; it stops being the product's entry point.
- **Public specs.** Acceptable: protocol tests are normative and shared. Behavioral tests are removed with the engine.
- **`flatc` on untrusted schemas.** Mitigated in Stage 6, not by running an agent sandbox.
- **Scope creep back into execution.** The non-goals are the guard: any future tool that needs an actor directory belongs to the example or to CI.

## 9. Open items

1. Rename `internal/harness` to `internal/protocol` while keeping the reflection/builder/mutation files? Relevant for clarity, not behavior.
2. Does `purpose.md` filtering belong in the resource template (`boundary://actor/{name}/context`) or stay in `get_boundary_context` only? Prefer both, one implementation.
3. Should the compatibility manifest be a resource or an on-demand tool? The manifest depends on a base version, which fits a tool better.
4. Where do generated SDKs live? `-sdk-out` stays; the server still owns regeneration on commit.
5. Deprecation window for `verify_actor_behavior`: remove immediately (no known external clients) or keep a stub returning `410`-style guidance for one release.
