# Boundary-MCP: Purpose-Driven Development Architecture with Deterministic Binary Contracts

## Contents

1. [Overview and Architectural Thesis](#1-overview-and-architectural-thesis)
2. [System Topology](#2-system-topology)
3. [Repository Directory Structure](#3-repository-directory-structure)
4. [Verbal Source of Truth Format](#4-verbal-source-of-truth-format)
5. [boundary-mcp Tool Specifications](#5-boundary-mcp-tool-specifications)
6. [Protocol Tests and the Reference Verifier](#6-protocol-tests-and-the-reference-verifier)
7. [Environment and Isolation](#7-environment-and-isolation)
8. [Implementation Roadmap (Go MVP)](#8-implementation-roadmap-go-mvp)

---

## 1. Overview and Architectural Thesis

**Boundary-MCP** is a control plane for building distributed systems based on the Actor Model (inspired by Erlang/BEAM) assisted by AI coding agents (such as `opencode`).

The system solves three structural failure modes in current LLM-assisted software engineering:

1. **Abstraction leaks via excessive visibility:** Agents that can read the source code of neighboring modules inevitably create implicit couplings.
2. **Semantic contract drift:** Agents tend to loosen schemas or alter signatures to force local tests to pass.
3. **Inference tax and non-determinism at runtime:** Purely verbal protocols on the critical execution path (such as MCP or A2A at runtime) degrade latency, cost, and precision.

### Core Principles

- **Semantic protocol management:** The contract is managed by a subjective engine that decides *what the protocol should mean* — intent fit, scope, invariants and drift. Deterministic gates decide whether that decision is expressible, wire-compatible and honored. The engine drives; the gates execute.
- **Separation of Design Plane and Data Plane:** The semantic engine and its gates operate exclusively at design/governance time. At *runtime*, processes (black boxes) communicate strictly via **FlatBuffers** (*zero-copy* binary) over IPC, Unix Domain Sockets, or NATS.
- **Capability-Based Isolation:** The user's coder runs in the actor's own service directory and connects over MCP. The control plane never receives actor code and exposes only that actor's boundary view. A `bwrap` runner for the disposability experiment lives under `examples/litmus/`.
- **Bidirectional Synchronization (`purpose.md` ↔ `protocol.fbs`):** The verbal purpose document and the binary schema co-evolve through a semantic compiler encapsulated behind deterministic gatekeepers (`flatc --conform`).
- **Blind Behavioral Verification (Black-Box Testing):** The reference verifier and CI induce and execute deterministic state-machine and property-based tests against actor binaries (both as client and server), hiding fuzzing vectors from the implementing coder to prevent overfitting.

---

## 2. System Topology

```text
+--------------------------------------------------------------------------+
|                        CONTROL PLANE (Governance)                        |
|                                                                          |
| +----------------------------------------------------------------------+ |
| |                       boundary-mcp (Go Binary)                       | |
| |                                                                      | |
| |   [purpose.md] <--> [Semantic Compiler (LLM)] <--> [protocol.fbs]    | |
| |                        |                           |                 | |
| |            [Semantic Engine (JEV gate)]        [flatc CLI]           | |
| |                        |                           |                 | |
| |            [Protocol Tests (resources)]     [Read-Only SDKs]         | |
| |                        |                                             | |
| |                  [History / Versions]                                | |
| +----------------------------------------------------------------------+ |
+-----------------^--------------------------------------^-----------------+
                  | MCP (stdio/HTTP)                     | MCP (stdio/HTTP)
                  |                                      |
+-----------------------------------+  +-----------------------------------+
|      CODER A (user-owned)         |  |      CODER B (user-owned)         |
|                                   |  |                                   |
|  - its own service directory (RW) |  |  - its own service directory (RW) |
|  - generated SDK (RO)             |  |  - generated SDK (RO)             |
|  - no visibility into Coder B     |  |  - no visibility into Coder A     |
|  - no access to governance        |  |  - no access to governance        |
+-----------------------------------+  +-----------------------------------+
                  |                                      |
                  v                                      v
+--------------------------------------------------------------------------+
|                        CI (merge gate)                                   |
|   builds each actor, runs its behavioral suite, blocks on failure        |
+--------------------------------------------------------------------------+
```

---

## 3. Repository Directory Structure

```text
boundary-system/
├── governance/                     # never exposed to coders
│   ├── purpose.md                  # Verbal source of truth (actors, flows, invariants)
│   ├── protocol.fbs                # Canonical FlatBuffers schema
│   ├── state_machine.json          # Induced state machine and invariants
│   ├── history/                    # Committed snapshots (artifacts + diff + meta)
│   └── protocol_tests/             # Normative specs served as resources
├── sdk/                            # Generated bindings consumed by the coder
│   ├── go/                         # Code generated via `flatc --go`
│   └── rust/                       # Code generated via `flatc --rust`
├── actors/                         # One service directory per actor (user-owned)
├── cmd/
│   ├── boundary-mcp/               # MCP server in Go
│   ├── judge-eval/                 # Labeled proposal benchmark for JEV
│   └── versionctl/                 # Content-addressed versions and aliases
├── examples/
│   └── litmus/                     # Two-actor fixture and reference verifier
└── internal/
    ├── compiler/                   # purpose.md <-> protocol.fbs semantic compiler
    ├── fbs/                        # Deterministic wrapper for `flatc`
    ├── governance/                 # purpose.md parser and actor filtering
    ├── judge/                      # Semantic gate (JEV)
    ├── llm/                        # OpenAI-compatible structured-output client
    ├── mcp/                        # MCP tools, resources and HTTP auth
    ├── protocol/                   # .bfbs reflection, frame builder, validation
    ├── protocoltest/               # Protocol test spec and deterministic expansion
    └── versioning/                 # Versions, aliases, compatibility manifests
```

---

## 4. Verbal Source of Truth Format

`governance/purpose.md` should be structured to support both human auditing and deterministic extraction of invariants and state machines.

Example:

```markdown
# System: Entity Enrichment Pipeline

## Actors
1. **actor_ingest** (Emitter / Client): Ingests raw batches, normalizes identifiers, and dispatches enrichment requests.
2. **actor_enrich** (Receiver / Server): Queries local storage engines (LMDB/RocksDB) and returns structured metadata.
3. **actor_external_crm** (External Adapter): Isolates an external legacy HTTP API, translating responses into the internal protocol.

## Protocol & State Flow
- **Initial State:** `IDLE`
- **Transition 1:** `actor_ingest` sends `EnrichRequest` -> State moves to `PENDING`.
- **Transition 2:** `actor_enrich` replies with `EnrichResponse` (success) or `EnrichError` (failure) -> State moves to `COMPLETED` or `FAILED`.

## Boundary Invariants (Hard Contracts)
- **INV-01:** `EnrichRequest.correlation_id` must be a valid non-empty UUIDv7.
- **INV-02:** `EnrichRequest.document_id` must contain exactly 14 numeric digits. Otherwise, `actor_enrich` MUST return `ErrorCode.INVALID_DOCUMENT` in under 5ms without hitting storage.
- **INV-03:** `EnrichResponse.confidence_score` must fall strictly within `[0.0, 1.0]`.
- **INV-04 (Idempotency):** Two `EnrichRequest` messages sharing the same `correlation_id` must yield byte-identical output payloads with zero duplicate side effects.
- **INV-05 (Client Resilience):** If `actor_enrich` fails to respond within 500ms, `actor_ingest` must perform at most 2 retries with exponential backoff before routing the event to a dead-letter topic.
```

---

## 5. boundary-mcp Tool Specifications

The `boundary-mcp` server exposes two actor-facing MCP tools (`get_boundary_context`, `propose_contract_evolution`) and one protocol-testing tool (`get_protocol_tests`), plus protocol resources addressed by content hash. It runs over stdio for direct sessions; remote sessions connect to a control-plane instance over Streamable HTTP with a bearer token. `-actor-token actor=token` scopes a token to one actor. `sync_from_manual_edit` (§5.4) is an administrative CLI trigger rather than an MCP tool.

### 5.1 `get_boundary_context`

Returns exclusively the boundary view relevant to the requesting actor.

**Input:**

```json
{ "actor_name": "string" }
```

**Behavior:**

1. Reads `governance/purpose.md` and filters only the actors, messages, and invariants touching `actor_name`.
2. Returns the full contents of `governance/protocol.fbs`.
3. Returns the SDK path advertised by `-sdk`; the coder's project consumes it read-only.

### 5.2 `propose_contract_evolution`

The sole mechanism through which an agent can request changes to the protocol or invariants.

**Input:**

```json
{
  "actor_name": "string",
  "intent_description": "string",
  "proposed_fbs_snippet": "string (optional)",
  "affects_invariants": ["INV-02"]
}
```

**Internal Pipeline (Hybrid: Semantic + Deterministic):**

1. **Semantic Analysis (Encapsulated Agent):** The MCP's internal LLM receives the current `purpose.md`, `protocol.fbs` and `state_machine.json`, plus the proposal. Using strict JSON Schema structured outputs, it returns the complete candidate contents of all three artifacts.
2. **Syntactic Gatekeeper (`flatc`):** Runs `flatc --binary --schema -o /tmp protocol.fbs.next` to validate syntax and types.
3. **Compatibility Gatekeeper (`flatc --conform`):** Runs `flatc --conform protocol.fbs protocol.fbs.next`.
   - **On failure (breaking change):** Automatically rejects the proposal and returns the exact `flatc` diagnostic to the agent (e.g., *"Field removed or ID altered; mark as deprecated instead of deleting"*).
4. **Bidirectional Commit:** Upon passing validation:
   - Writes `governance/protocol.fbs`, `governance/purpose.md` and `governance/state_machine.json` atomically.
   - Saves a snapshot under `governance/history/<timestamp>_<actor>/` with the three artifacts, a human-readable `diff.patch` and a `meta.json` (intent, direction, model, affected invariants).
   - Recompiles the configured SDK targets.
   - Runs the `OnCommit` hook; the default binary registers none.

### 5.3 `get_protocol_tests` and protocol resources

`get_protocol_tests` returns the normative protocol test specs for the active content hash. It is the tool fallback for clients that do not read resources.

**Input:** none.

**Behavior:** validates every spec under `governance/protocol_tests/*.json` and returns `{ "protocol": "<hash>", "specs": { "<name>": <spec> } }`.

The same content is available as resources addressed by the bundle content hash, so a URI always names the same contract:

| URI | Content |
| --- | --- |
| `boundary://protocol/{hash}/fbs` | `protocol.fbs` and, when present, the compiled `.bfbs` |
| `boundary://protocol/{hash}/purpose` | the full `purpose.md` |
| `boundary://protocol/{hash}/tests` | the normative protocol test specs |
| `boundary://actor/{actor}/context` | the filtered boundary view for one actor |

The server serves only the active hash. Historical versions are a follow-up.

### 5.4 `sync_from_manual_edit` (Administrative Trigger / CLI)

Invoked by the human architect (or a file watcher) whenever `purpose.md` or `protocol.fbs` is edited directly.

1. Detects which artifact was modified most recently (mtime / SHA-256 hash).
2. If `purpose.md` changed → compiles updates to `protocol.fbs` and `state_machine.json`.
3. If `protocol.fbs` changed → decompiles the semantic diff and updates `purpose.md` and `state_machine.json`.

### 5.5 Versioned evolution (prototype)

[ADR-0002](adr/0002-content-addressed-protocol-versions.md) adds a content-addressed layer around the auto-commit path. A version is the SHA-256 hash of `purpose.md`, `protocol.fbs` and `state_machine.json`; actors pin the version they adopted, and `versionctl` records human aliases such as `v0`.

Compatibility is a relation between a base and a candidate version. `flatc --conform` decides wire compatibility; a reflection diff records structural changes; the semantic engine will classify semantic and role-specific impact (`producer_only`, `consumer_only`, `requires_peer_upgrade`). A required field added to an existing table is a concrete case that `flatc` accepts but must be treated as breaking for old peers.

The current prototype is read-only with respect to the active contract: `snapshot`, `compat`, `list`, `show`, `alias` and `pin`. Draft creation, promotion, abandonment, the wire-version field and the pairwise adoption matrix are follow-ups. The compiler still commits proposals directly; the draft lifecycle is not yet wired into `propose_contract_evolution`.

The end-to-end workflows — actor development, current auto-commit, designed draft/promote/abandon, compatibility classes, adoption and breaking changes — are documented in [WORKFLOWS.md](WORKFLOWS.md).

---

## 6. Protocol Tests and the Reference Verifier

The system has two test layers with different owners:

| Layer | Owner | Nature |
| --- | --- | --- |
| Protocol tests | boundary-mcp publishes; coder and CI run | Declarative, normative, no process |
| Behavioral verification | user's CI (reference implementation in `examples/litmus/harness`) | Builds and runs actor binaries |

### 6.1 Protocol test specs

A spec is declarative data, not a runner. The server validates `governance/protocol_tests/*.json` and serves them as resources. A spec contains:

- `seeds`: valid frames (table + values) that structural cases derive from;
- `rules`: semantic assertions with explicit valid/invalid examples, plus optional `regex`, `format`, `min` and `max` used to validate those examples;
- `mutations`: structural categories to generate (`truncate`, `bad-offset`, `invalid-utf8`, `flip`, `scalar-bounds`);
- `roundtrip`: whether the runner must decode the frame and read the fields back;
- `generation.cases`: an optional consistency check on the expanded count.

`internal/protocoltest` expands a spec deterministically into base64 frames with `accept`/`reject` expectations and semantic assertions, using `.bfbs` reflection, the dynamic builder and structural mutations from `internal/protocol`. Two runners given the same spec and schema produce the same case set.

The server never expands, builds or executes anything for a spec. Expansion belongs to the coder and CI.

### 6.2 Reference behavioral verifier

`examples/litmus/harness` is the reference implementation of the behavioral layer. It is example infrastructure and the basis for the litmus CI, not an MCP tool.

- **Server mode:** the verifier acts as a Harness Client, sends valid seeds and malformed frames, and checks that the actor never panics and drops or types-invalidates input correctly.
- **Client mode:** the verifier acts as a Reference Mock Peer, scripts request/response scenarios and checks emitted frames with declarative assertions (`regex`, `min`, `max`, `equals`).
- Frames are built dynamically from `.bfbs`: suites carry field values, not base64 payloads.
- `build` and `run` are trusted suite values. The verifier is not agent-facing.

Example suite:

```json
{
  "mode": "server",
  "schema": "protocol.bfbs",
  "response_table": "EnrichResponse",
  "build": "go build -o bin/actor ./...",
  "run": ["./bin/actor", "--socket", "{socket}"],
  "timeout_ms": 2000,
  "seeds": [
    {"table": "EnrichRequest", "values": {"document_id": "12345678901234"}}
  ]
}
```

- The actor receives `BOUNDARY_SOCKET`, and `{socket}` placeholders are substituted in `run`.
- Property tests for the protocol core use `pgregory.net/rapid`.

### 6.3 External actors

When a node is an external service, its actor is a thin boundary adapter. The behavioral suite can point it at a local HTTP/gRPC mock, so translation into internal FlatBuffers stays deterministic and no external schema leaks into the internal protocol.

### 6.4 Ownership

- The server owns the spec and its version.
- The coder runs the spec during development.
- CI runs the behavioral suite and decides whether a merge proceeds.

---

## 7. Environment and Isolation

The control plane does not launch or sandbox the coder. The coder is user-owned and runs in the actor's service directory, as it does for any microservice.

Isolation is split by owner:

| Layer | Owner | Mechanism |
| --- | --- | --- |
| Actor source visibility | user's coder | one service directory per actor; `external_directory: deny` |
| Boundary data exposure | boundary-mcp | filtered purpose, active protocol and protocol tests; never sibling data or hidden suites |
| Interface access | boundary-mcp | `-actor-token actor=token` scopes a bearer to one actor; `-token` is unscoped |
| Code execution | user's CI | the control plane never builds or runs actor code |

The litmus example keeps a reference runner under `examples/litmus/` that reproduces the Phase 5 disposability experiment with **Bubblewrap (`bwrap`)**: it mounts only the actor workspace and the read-only SDK, keeps `governance/` outside the mount namespace, and connects over a bearer-protected loopback endpoint. It is example infrastructure, not the supported deployment model.

> The example runner shares the host network namespace to reach the loopback endpoint. Sandboxing it further is tracked in the plan.

`flatc` runs with a 30-second default timeout and rejects schemas above 1 MiB; `.fbs` content is untrusted input to a C++ parser. A tool response never includes an absolute governance path: the proposal result reports `diff.patch` relative to the governance directory.

---

## 8. Implementation Roadmap (Go MVP)

> The phase numbering records **construction order, not architecture hierarchy**. The semantic engine is the protocol-management core from the moment the semantic compiler is enabled ([ADR-0003](adr/0003-semantic-engine-as-protocol-management-core.md)); later phases extend it rather than adding it on top.

### Phase 1: Deterministic Core & CLI (`internal/fbs`)

- [x] Initialize Go module (`go mod init boundary-mcp`).
- [x] Implement wrapper around `flatc`:
  - [x] Code generation (`--go`, `--rust`).
  - [x] Schema evolution enforcement (`flatc --conform old.fbs new.fbs`).
- [x] Implement standard binary framing (`[4-byte length][FlatBuffer payload]`) for transport-agnostic IPC (later moved to `examples/litmus/harness/`).

### Phase 2: MCP Server & Reference Runner (`internal/mcp` & `examples/litmus/`)

- [x] Build Go MCP server (using `github.com/mark3labs/mcp-go`) exposing `get_boundary_context` and `propose_contract_evolution` over stdio and loopback Streamable HTTP (protocol resources and `get_protocol_tests` were added later).
- [x] Create the sandboxed reference runner with `bwrap` and an `opencode.json` template wired to the `boundary-mcp` binary (in `examples/litmus/`).

### Phase 3: Bidirectional Semantic Compiler (`internal/compiler`)

- [x] Integrate an LLM client (OpenAI-compatible, strict JSON Schema structured outputs) inside `boundary-mcp` to translate `purpose.md` ↔ `protocol.fbs` + `state_machine.json`.
- [x] Implement audit logging in `governance/history/` with human-readable unified diffs and per-commit metadata.

### Phase 4: Boundary Test Engine (`internal/protocol`, `examples/litmus/harness`)

- [x] Implement `.bfbs` reflection (tables, fields, types, enums, defaults), a dynamic FlatBuffer builder for seed/response frames, and structural mutation generation (boundary scalars, invalid UTF-8, truncation, corrupted offsets), with `pgregory.net/rapid` property tests.
- [x] Implement the Reference Mock Peer (state-machine mock) used to verify client actors, plus the Harness Client for receiver actors, over length-prefixed Unix-socket framing.
- [x] Wire the harness into the actor verification path (later moved out of the MCP server to `examples/litmus/verify`) with a JSON suite/run convention, dynamic seed frames and scripted peer scenarios.
- [ ] Sandbox the verifier's build/run subprocesses (for example with `bwrap`) before using it on untrusted actor code.

### Phase 5: Litmus Test (Thesis Validation)

- [x] Author a real `purpose.md` spanning 2 actors (`actor_ingest` in Go + `actor_enrich` in Go). See `examples/litmus/`.
- [x] Implement both actors and their hidden suites; `actor_enrich` was then wiped and reconstructed by a sandboxed `opencode` session from boundary context only.
- [x] **Disposability Test (`actor_enrich`):** PASS — the reconstructed actor builds and passes the hidden boundary suite (11 cases) without access to `actor_ingest`.
- [ ] Repeat the reconstruction test for `actor_ingest` (optional full litmus).

`examples/litmus/` is the Phase 5 fixture: contract, two Go actors, hidden suites, `make litmus`, and `scripts/disposability.sh` for the destructive reconstruction run.

### Core layer: Semantic engine (ADR-0001, ADR-0003)

The semantic engine (JEV / System One via OpenRouter's typed System One API) is the protocol-management core. It answers the question `flatc` cannot: is this the right protocol? It catches semantic drift — intent mismatch, out-of-scope edits, dropped invariants, purpose/protocol divergence — and enforces a policy over calibrated judgments. It is mandatory whenever the semantic compiler is enabled and fails closed.

Its authority is bounded by domain, not by rank: `flatc` remains the conformance oracle for syntax and wire compatibility, and the harness remains the behavior oracle. A semantic verdict can never approve a schema that does not compile or an actor that fails its suite. See [ADR-0003](adr/0003-semantic-engine-as-protocol-management-core.md) for the layer model and [ADR-0001](adr/0001-advisory-semantic-judge.md) for the gate decision.

### Prototype: Versioned Evolution (ADR-0002)

- [x] Content-addressed bundle and wire hashes.
- [x] Immutable version snapshots, aliases and actor adoption pins.
- [x] `flatc --conform` plus reflection-diff compatibility manifests.
- [x] `versionctl` CLI: `snapshot`, `compat`, `list`, `show`, `alias`, `pin`.
- [ ] JEV semantic/role classifier (`producer_only`, `consumer_only`, `requires_peer_upgrade`).
- [ ] Draft/promote/abandon lifecycle in the compiler and MCP tools.
- [ ] `protocol_version:uint` in the envelope.
- [ ] Pairwise adoption matrix and promotion policy.

---

### Implementation Note for Phase 4

One particularly useful feature of `flatc` for `internal/protocol` is **`--binary --schema`**, which compiles any `.fbs` into a self-describing `.bfbs` (*Binary FlatBuffer Schema*). Using FlatBuffers' official reflection schema (`reflection.fbs`), the Go harness can **dynamically inspect tables, field types, enums, and default values at runtime** without recompiling `boundary-mcp` whenever a contract evolves — making the structured fuzzer and Reference Mock Peer in the example generic across any protocol you define.
