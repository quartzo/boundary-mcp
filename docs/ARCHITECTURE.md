# Boundary-MCP: Purpose-Driven Development Architecture with Deterministic Binary Contracts

> **Scope change in progress (2026-11-25).** This specification still describes the actor runtime and actor-behavior verification that [ADR-0004](adr/0004-protocol-only-mcp.md) removes from the control plane. §6 (test induction) and §7 (physical isolation) are the sections most affected. The target architecture and staged work are in [PLAN.md](PLAN.md); treat this document as the current implementation until the plan's stages land.

## Contents

1. [Overview and Architectural Thesis](#1-overview-and-architectural-thesis)
2. [System Topology](#2-system-topology)
3. [Repository Directory Structure](#3-repository-directory-structure)
4. [Verbal Source of Truth Format](#4-verbal-source-of-truth-format)
5. [boundary-mcp Tool Specifications](#5-boundary-mcp-tool-specifications)
6. [Deterministic Test Induction and Execution Engine](#6-deterministic-test-induction-and-execution-engine)
7. [Physical Environment Isolation](#7-physical-environment-isolation)
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
- **Capability-Based Physical Isolation:** Each actor is developed inside an isolated agent session (`opencode` running inside a `bwrap`/container sandbox) that **cannot see** the source code of other actors and lacks write permissions to contracts.
- **Bidirectional Synchronization (`purpose.md` ↔ `protocol.fbs`):** The verbal purpose document and the binary schema co-evolve through a semantic compiler encapsulated behind deterministic gatekeepers (`flatc --conform`).
- **Blind Behavioral Verification (Black-Box Testing):** The MCP server induces and executes deterministic state-machine and property-based tests against actor binaries (both as client and server), hiding fuzzing vectors from the implementing agent to prevent overfitting.

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
| |            [Boundary Test Generator]       [Read-Only SDKs]          | |
| |                        |                                             | |
| |         [Deterministic Harness (rapid)]                              | |
| +----------------------------------------------------------------------+ |
+-----------------^--------------------------------------^-----------------+
                  | MCP (stdio/HTTP)                     | MCP (stdio/HTTP)
                  | MCP (Boundary)                       | MCP (Boundary)
+-----------------------------------+  +-----------------------------------+
|     ACTOR A SANDBOX (opencode)    |  |     ACTOR B SANDBOX (opencode)    |
|                                   |  |                                   |
|  - /workspace/actor_a (RW)        |  |  - /workspace/actor_b (RW)        |
|  - /sdk/generated (RO)            |  |  - /sdk/generated (RO)            |
|  - No visibility into Actor B     |  |  - No visibility into Actor A     |
|  - No access to hidden tests      |  |  - No access to hidden tests      |
+-----------------------------------+  +-----------------------------------+

===========================================================================
                       DATA PLANE (Runtime / Production)
===========================================================================

  [Actor A Binary] =====( Zero-Copy FlatBuffers / NATS / IPC )=====> [Actor B Binary]
```

---

## 3. Repository Directory Structure

```text
boundary-system/
├── governance/                     # INVISIBLE to actor sandboxes
│   ├── purpose.md                  # Verbal source of truth (actors, flows, invariants)
│   ├── protocol.fbs                # Canonical FlatBuffers schema
│   ├── state_machine.json          # Induced state machine and invariants
│   ├── history/                    # Versioned snapshots (purpose + fbs + diff)
│   └── hidden_tests/               # Generated property-based test suites & oracles
├── sdk/                            # Mounted as READ-ONLY inside sandboxes
│   ├── go/                         # Code generated via `flatc --go`
│   └── rust/                       # Code generated via `flatc --rust`
├── actors/                         # Each subdirectory is mounted in isolation
│   ├── actor_ingest/               # Sandbox 1 (e.g., Go)
│   └── actor_enrich/               # Sandbox 2 (e.g., Rust or Go)
├── cmd/
│   ├── boundary-mcp/               # Main MCP server in Go
│   └── sandbox-run/                # bwrap/container wrapper to launch opencode
└── internal/
    ├── compiler/                   # Bidirectional bridge purpose.md <-> protocol.fbs
    ├── fbs/                        # Deterministic wrapper for `flatc` and `--conform`
    ├── harness/                    # Test runner (Actor as Server and Client)
    └── mcp/                        # MCP tool definitions and handlers
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

The `boundary-mcp` server exposes two actor-facing MCP tools (`get_boundary_context`, `propose_contract_evolution`) and one protocol-testing tool (`get_protocol_tests`), plus protocol resources addressed by content hash. It runs over stdio for direct sessions; remote sessions connect to a control-plane instance over Streamable HTTP with a bearer token. `sync_from_manual_edit` (§5.4) is an administrative CLI trigger rather than an MCP tool.

### 5.1 `get_boundary_context`

Returns exclusively the boundary view relevant to the requesting actor.

**Input:**

```json
{ "actor_name": "string" }
```

**Behavior:**

1. Reads `governance/purpose.md` and filters only the actors, messages, and invariants touching `actor_name`.
2. Returns the full contents of `governance/protocol.fbs`.
3. Returns the local mount path where compiled bindings reside in read-only mode (`/sdk/...`).

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
   - Recompiles SDKs into `/sdk/go` and `/sdk/rust` (host paths: `sdk/go` and `sdk/rust`).
   - Regenerates boundary tests in `governance/protocol_tests/` (Phase 4 hook).
   - Marks dependent consumer actors as `STALE` in the MCP's internal state (Phase 4 hook).

### 5.3 `verify_actor_behavior` (removed)

`verify_actor_behavior` is no longer an MCP tool. The server does not build or run actor code. Behavioral verification is a reference tool (`internal/harness`, driven by `examples/litmus/verify`) that the user's CI runs against the actor directory. See [ADR-0004](adr/0004-protocol-only-mcp.md) and [PLAN.md](PLAN.md).

### 5.4 `sync_from_manual_edit` (Administrative Trigger / CLI)

Invoked by the human architect (or a file watcher) whenever `purpose.md` or `protocol.fbs` is edited directly.

1. Detects which artifact was modified most recently (mtime / SHA-256 hash).
2. If `purpose.md` changed → compiles updates to `protocol.fbs` + tests.
3. If `protocol.fbs` changed → decompiles the semantic diff and updates `purpose.md` + tests.

### 5.5 Versioned evolution (prototype)

[ADR-0002](adr/0002-content-addressed-protocol-versions.md) adds a content-addressed layer around the auto-commit path. A version is the SHA-256 hash of `purpose.md`, `protocol.fbs` and `state_machine.json`; actors pin the version they adopted, and `versionctl` records human aliases such as `v0`.

Compatibility is a relation between a base and a candidate version. `flatc --conform` decides wire compatibility; a reflection diff records structural changes; the semantic engine will classify semantic and role-specific impact (`producer_only`, `consumer_only`, `requires_peer_upgrade`). A required field added to an existing table is a concrete case that `flatc` accepts but must be treated as breaking for old peers.

The current prototype is read-only with respect to the active contract: `snapshot`, `compat`, `list`, `show`, `alias` and `pin`. Draft creation, promotion, abandonment, the wire-version field and the pairwise adoption matrix are follow-ups. The compiler still commits proposals directly; the draft lifecycle is not yet wired into `propose_contract_evolution`.

The end-to-end workflows — actor development, current auto-commit, designed draft/promote/abandon, compatibility classes, adoption and breaking changes — are documented in [WORKFLOWS.md](WORKFLOWS.md).

---

## 6. Deterministic Test Induction and Execution Engine

To ensure black-box actors respect both structural types and temporal semantics, `internal/harness` implements two verification modes driven by `.fbs` reflection and `state_machine.json`. The package parses `.bfbs` into an in-memory schema, validates payloads, generates structural mutations and exposes a **Harness Client** (receivers) and a **Reference Mock Peer** (emitters) over length-prefixed Unix-socket framing.

### 6.1 Testing an Actor as Server (Receiver)

`boundary-mcp` acts as a **Harness Client + Structured Fuzzer** (using `pgregory.net/rapid` in Go):

- The actor binary is spawned as an isolated child process attached to binary IPC channels (Unix Domain Socket or length-prefixed `stdin`/`stdout` framing: `uint32` little-endian length + FlatBuffer payload).
- **Structural Mutation Tests:** Dispatches valid buffers, buffers with omitted optional fields, extreme Unicode strings, boundary numeric values (`0`, `-1`, `MaxUint64`), and truncated/corrupted frames, verifying that the actor never panics and cleanly drops malformed input or returns a typed error.
- **Domain Invariant Tests:** For each `INV-XX` extracted from `purpose.md`, the generator synthesizes targeted inputs.
  - Example (`INV-02`): Generates 100 `EnrichRequest` frames with `document_id` lengths of `0`, `13`, `15` and non-numeric characters, asserting 100% return `ErrorCode.INVALID_DOCUMENT` with latency `< 5ms`.
- **Idempotency Tests (`INV-04`):** Replays the exact same binary frame *N* times and asserts byte-for-byte identical response buffers.

### 6.2 Testing an Actor as Client (Emitter)

`boundary-mcp` spins up a **Reference Mock Peer** — a state-machine mock that implements the contract as the reference counterpart. The mock is the *test oracle*: it knows the expected transitions and returns the verdict.

1. The client actor is launched and pointed at the Reference Mock Peer's socket.
2. The mock peer executes a battery of protocol scenarios derived from `state_machine.json`:
   - **Happy Path:** Accepts `EnrichRequest`, validates that outgoing fields satisfy `INV-01` and `INV-03`, and emits `EnrichResponse`.
   - **Domain Error Path:** Returns `EnrichError` and verifies the client transitions cleanly to a terminal state without infinite retry loops.
   - **Latency / Timeout Path (`INV-05`):** Deliberately holds the response for 600ms and counts retry attempts, failing the test if the client exceeds 2 retries or violates backoff bounds.
   - **Sequence Violation Path:** Injects an out-of-order frame or unknown `correlation_id` and verifies the client rejects it without corrupting internal state.

### 6.3 Strategy for External Actors (Legacy Systems / Third-Party APIs)

When a node in the topology represents an external service that does not natively speak FlatBuffers:

- `actors/actor_external_crm` houses strictly a thin **Boundary Adapter** (Anti-Corruption Layer).
- `boundary-mcp` verifies this adapter against a local HTTP/gRPC mock simulating real upstream payloads and fault modes, guaranteeing that translation into internal FlatBuffers is 100% deterministic and free of external schema leakage.

### 6.4 Hidden Test Suites (`governance/hidden_tests/<actor>.json`)

The reference verifier (`examples/litmus/verify`) loads a trusted suite per actor:

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

- Frames are **built dynamically** from `.bfbs`: suites carry field values, not base64 payloads.
- `mode: "server"` runs the Harness Client; `mode: "client"` runs the Reference Mock Peer with `scenarios` and declarative checks (`regex`, `min`, `max`, `equals`).
- The actor receives `BOUNDARY_SOCKET`, and `{socket}` placeholders are substituted in `run`.
- `build` and `run` are trusted governance values. `entrypoint_cmd` from the agent is only a fallback; pin `build` in the suite so the agent cannot execute arbitrary host commands.

---

## 7. Physical Environment Isolation

To prevent LLMs from bypassing boundaries by reading neighboring files, each `opencode` session is launched via `cmd/sandbox-run/sandbox-run.sh`, which uses **Bubblewrap (`bwrap`)** on Linux:

```bash
make build
./cmd/sandbox-run/sandbox-run.sh actor_enrich
```

The launcher:

1. Starts `boundary-mcp` **outside** the sandbox (`-transport http -addr 127.0.0.1:0 -token ...`) with access to `governance/`.
2. Waits for the server's `-ready-file`, then renders `opencode.json` pointing at the loopback Streamable HTTP endpoint.
3. Runs `bwrap` with the actor workspace, the read-only SDK, the system runtime, and opencode's own state directories.
4. Tears the server down when the session exits.

**Sandbox Guarantees:**

- `/workspace` mounts exclusively `actors/$ACTOR`.
- `governance/`, the control-plane binary, and `actors/<others>` do not exist inside the process's mount namespace.
- `sdk/` is mounted read-only (`--ro-bind`); any attempt by the agent to edit generated `.go` or `.rs` bindings fails at the kernel level (`EROFS`).
- The only bridge to the control plane is a bearer-protected loopback endpoint; governance files never enter the sandbox.

> **Note:** the sandbox shares the host network namespace so it can reach the loopback endpoint. The endpoint requires a per-session bearer token generated by `sandbox-run`.

---

## 8. Implementation Roadmap (Go MVP)

> The phase numbering records **construction order, not architecture hierarchy**. The semantic engine is the protocol-management core from the moment the semantic compiler is enabled ([ADR-0003](adr/0003-semantic-engine-as-protocol-management-core.md)); later phases extend it rather than adding it on top.

### Phase 1: Deterministic Core & CLI (`internal/fbs`)

- [x] Initialize Go module (`go mod init boundary-mcp`).
- [x] Implement wrapper around `flatc`:
  - [x] Code generation (`--go`, `--rust`).
  - [x] Schema evolution enforcement (`flatc --conform old.fbs new.fbs`).
- [x] Implement standard binary framing (`[4-byte length][FlatBuffer payload]`) for transport-agnostic IPC (Unix Socket / stdio / NATS).

### Phase 2: MCP Server & Sandbox (`internal/mcp` & `cmd/sandbox-run`)

- [x] Build Go MCP server (using `github.com/mark3labs/mcp-go`) exposing `get_boundary_context` and `propose_contract_evolution` over stdio and loopback Streamable HTTP (protocol resources and `get_protocol_tests` were added later).
- [x] Create `sandbox-run` wrapper with `bwrap` and an `opencode.json` template wired to the `boundary-mcp` binary, keeping governance outside the sandbox mount namespace.

### Phase 3: Bidirectional Semantic Compiler (`internal/compiler`)

- [x] Integrate an LLM client (OpenAI-compatible, strict JSON Schema structured outputs) inside `boundary-mcp` to translate `purpose.md` ↔ `protocol.fbs` + `state_machine.json`.
- [x] Implement audit logging in `governance/history/` with human-readable unified diffs and per-commit metadata.

### Phase 4: Boundary Test Inducer (`internal/harness`)

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

One particularly useful feature of `flatc` for `internal/harness` is **`--binary --schema`**, which compiles any `.fbs` into a self-describing `.bfbs` (*Binary FlatBuffer Schema*). Using FlatBuffers' official reflection schema (`reflection.fbs`), the Go harness can **dynamically inspect tables, field types, enums, and default values at runtime** without recompiling `boundary-mcp` whenever a contract evolves — making the structured fuzzer and Reference Mock Peer generic across any protocol you define.
