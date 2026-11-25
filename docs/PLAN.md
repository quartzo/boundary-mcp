# Plan: Protocol-Only boundary-mcp

- **Status:** Stages 1–7 implemented
- **Date:** 2026-11-25
- **Decision record:** [ADR-0004](adr/0004-protocol-only-mcp.md)
- **Supersedes in scope:** the actor-runtime and actor-verification parts of [ARCHITECTURE.md](ARCHITECTURE.md) §6–§7 and [WORKFLOWS.md](WORKFLOWS.md) §2–§3

## 1. Why this plan exists

The repository currently carries three responsibilities:

1. **Contract management** — `purpose.md` ↔ `protocol.fbs` ↔ `state_machine.json`, `flatc` gates, the semantic engine, versions and compatibility.
2. **Agent runtime** — `cmd/sandbox-run` launched a pinned `opencode` inside `bwrap`.
3. **Actor behavior verification** — `verify_actor_behavior` builds and runs the actor binary against hidden suites.

Only (1) is the product. (2) and (3) were introduced to make the Phase 5 disposability experiment reproducible, and were then documented as the product's isolation and verification models. That inversion has concrete costs:

- the coder of the user became a dependency of the contract manager;
- `verify_actor_behavior` executed code the server did not write (`examples/litmus/harness/verifier.go:103` before the move), and accepted an agent-supplied `entrypoint_cmd` (`:75-80`);
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

The generator reuses the existing, actor-free pieces: `.bfbs` reflection (`internal/protocol/schema.go`), frame building (`build.go`) and structural mutations (`payload.go`).

## 5. What moves, what is deleted

| Path | Action |
| --- | --- |
| `internal/protocol/` (`schema.go`, `fb.go`, `payload.go`, `build.go`) | the protocol-test core; renamed from `internal/harness` |
| `examples/litmus/harness/` (`verifier.go`, `client.go`, `peer.go`, `suite.go`, `checks.go`, `wire.go`) | the behavioral engine |
| `internal/fbs/framing.go` | moved to `examples/litmus/harness/framing.go` |
| `examples/litmus/governance/protocol_tests/` | normative protocol test spec |
| `examples/litmus/governance/hidden_tests/` | stays as fixture data for the reference verifier |
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
- `examples/litmus/governance/protocol_tests/protocol.json` sits next to `hidden_tests/`, which remains as fixture data for the reference verifier.

**Acceptance:** `boundary://protocol/{hash}/tests` returns the validated specs; repeated `protocoltest.Expand` of the same spec is byte-identical; no spec key implies a process.

### Stage 2 — Remove the execution path (implemented)

- `verify_actor_behavior`, `BehaviorVerifier`, the verifier wiring and the `-actors`/`-hidden-tests` flags are gone.
- `harness` no longer depends on `mcp`; the reference verifier is used directly by `examples/litmus/verify`.
- No `exec.Command` remains under `internal/mcp`, `internal/compiler` or `internal/versioning` except `flatc`.

**Acceptance:** the server builds and serves its tools and resources with no actor directory present; no tool runs a subprocess.

### Stage 3 — Split the harness (implemented)

- The core moved to `internal/protocol`: `.bfbs` reflection, frame validation and reading, structural mutations and the dynamic builder.
- The behavioral engine moved to `examples/litmus/harness`: verifier, client, peer, suites, checks, wire and the length-prefixed framing that used to live in `internal/fbs`.
- `examples/litmus/verify` imports the example package; `internal/versioning` and `internal/protocoltest` import `internal/protocol`.

**Acceptance:** `go build ./...` and `go test ./...` pass; the litmus example still runs end to end.

### Stage 4 — Move the agent runtime out (implemented)

- `cmd/sandbox-run` moved to `examples/litmus/sandbox-run/`; its default root is now the example and its binary default is `<repo>/bin/boundary-mcp`.
- The README quick start and the Sandbox section were replaced by "Connecting a coder" (MCP endpoint, `{env:...}` token, `external_directory: deny`).
- `examples/litmus/scripts/disposability.sh` invokes the moved runner.

**Acceptance:** no product path mentions `bwrap`; the only references are the example and this plan.

### Stage 5 — Rewrite the docs (implemented)

- README, ARCHITECTURE and WORKFLOWS now describe the protocol manager: contract evolution, published protocol tests, the external coder and the CI gate.
- ARCHITECTURE §5 documents `get_protocol_tests` and the resources; §6 separates protocol tests from the reference behavioral verifier; §7 states the owner-per-layer isolation model.
- WORKFLOWS §2 starts at the user's coder and §12 no longer exposes a sandboxed product command.
- The "scope change in progress" banners are gone.

**Acceptance:** `rg 'verify_actor_behavior|sandbox-run|physical isolation'` matches only historical ADRs, this plan, and `examples/`. Met.

### Stage 6 — Hardening that remains (implemented)

- `-actor-token actor=token` binds the HTTP bearer to one actor; the middleware puts the actor in the request context and `authorizeActor` rejects tools and resources for a different one. `-token` remains an unscoped option.
- `actor_name` is validated against the Actors section of `purpose.md` before the compiler runs.
- `flatc` gets a 30-second default timeout when the caller has no deadline, and schemas above 1 MiB are rejected.
- The proposal result reports `diff.patch` relative to the governance directory instead of an absolute host path.

**Acceptance:** a token for `actor_ingest` cannot read, propose or read the context resource for `actor_enrich`; covered by `TestScopedTokenOverHTTP`.

### Stage 7 — Make the gate explicit (implemented)

- `examples/litmus/protocoltest` is the reference runner: it expands each spec twice, fails on non-determinism, validates every structural case against the schema and reports the semantic assertions.
- `make protocol-test` runs it; `make litmus` now runs it before the behavioral acceptance example.
- Merge authority is documented as the user's CI in README, ARCHITECTURE §6.4 and WORKFLOWS; the server is not described as a gate.
- The runner exposed two unreliable mutations, which were fixed in `internal/protocol`: `truncate-root` replaces `truncate-last`, and `flip-root-offset` replaces the padding-sensitive `flip-last-byte`.

**Acceptance:** the reference runner reproduces the spec's cases byte-for-byte and fails on structural drift; it is the only component described as a gate besides CI. Met.

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

1. Resolved in Stage 3: the core is `internal/protocol`; the behavioral engine is `examples/litmus/harness`.
2. Does `purpose.md` filtering belong in the resource template (`boundary://actor/{name}/context`) or stay in `get_boundary_context` only? Prefer both, one implementation.
3. Should the compatibility manifest be a resource or an on-demand tool? The manifest depends on a base version, which fits a tool better.
4. Where do generated SDKs live? `-sdk-out` stays; the server still owns regeneration on commit.
5. Resolved in Stage 2: `verify_actor_behavior` was removed immediately; there were no external clients.
