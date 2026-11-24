# Boundary-MCP: Purpose-Driven Development Architecture with Deterministic Binary Contracts

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

- **Separation of Design Plane and Data Plane:** Natural language and LLMs operate exclusively at design/governance time. At *runtime*, processes (black boxes) communicate strictly via **FlatBuffers** (*zero-copy* binary) over IPC, Unix Domain Sockets, or NATS.
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
| |            [Boundary Test Generator]          [flatc CLI]            | |
| |                        |                           |                 | |
| |         [Deterministic Harness (rapid)]    [Read-Only SDKs]          | |
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

The `boundary-mcp` server exposes 3 actor-facing MCP tools. It runs over stdio for direct sessions; sandboxed sessions connect to a control-plane instance over loopback Streamable HTTP with a per-session bearer token (see §7). The fourth entry, `sync_from_manual_edit` (§5.4), is an administrative CLI trigger rather than an MCP tool.

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

1. **Semantic Analysis (Encapsulated Agent):** The MCP's internal LLM receives the current `purpose.md`, the current `protocol.fbs`, and the proposal. It emits a candidate `protocol.fbs.next` and a textual diff `purpose.md.next`.
2. **Syntactic Gatekeeper (`flatc`):** Runs `flatc --binary --schema -o /tmp protocol.fbs.next` to validate syntax and types.
3. **Compatibility Gatekeeper (`flatc --conform`):** Runs `flatc --conform protocol.fbs protocol.fbs.next`.
   - **On failure (breaking change):** Automatically rejects the proposal and returns the exact `flatc` diagnostic to the agent (e.g., *"Field removed or ID altered; mark as deprecated instead of deleting"*).
4. **Bidirectional Commit:** Upon passing validation:
   - Writes `governance/protocol.fbs` and `governance/purpose.md`.
   - Saves a snapshot to `governance/history/<timestamp>_<actor>.patch`.
   - Recompiles SDKs into `/sdk/go` and `/sdk/rust`.
   - Regenerates boundary tests in `governance/hidden_tests/`.
   - Marks dependent consumer actors as `STALE` in the MCP's internal state.

### 5.3 `verify_actor_behavior`

Compiles the actor binary and subjects it to the hidden suite of deterministic tests.

**Input:**

```json
{ "actor_name": "string", "entrypoint_cmd": "string" }
```

**Behavior:**

1. Compiles the code inside the actor's directory (e.g., `go build` or `cargo build --release`).
2. Runs the **Server Harness** (if the actor receives messages) and/or the **Client Harness** (if the actor initiates messages) using the hidden suites in `governance/hidden_tests/`.

**Return Value to Agent:**

- **On pass:**

  ```json
  { "status": "PASS", "cases_executed": 500, "p99_latency_us": 42 }
  ```

- **On failure:** Returns only the minimal counterexample (input payload rendered as readable JSON, actual response observed, violated invariant `INV-XX`), never the source code of the test generator.

### 5.4 `sync_from_manual_edit` (Administrative Trigger / CLI)

Invoked by the human architect (or a file watcher) whenever `purpose.md` or `protocol.fbs` is edited directly.

1. Detects which artifact was modified most recently (mtime / SHA-256 hash).
2. If `purpose.md` changed → compiles updates to `protocol.fbs` + tests.
3. If `protocol.fbs` changed → decompiles the semantic diff and updates `purpose.md` + tests.

---

## 6. Deterministic Test Induction and Execution Engine

To ensure black-box actors respect both structural types and temporal semantics, `internal/harness` implements two verification modes driven by `.fbs` reflection and `state_machine.json`.

### 6.1 Testing an Actor as Server (Receiver)

`boundary-mcp` acts as an **Oracle Client + Structured Fuzzer** (using `pgregory.net/rapid` in Go):

- The actor binary is spawned as an isolated child process attached to binary IPC channels (Unix Domain Socket or length-prefixed `stdin`/`stdout` framing: `uint32` little-endian length + FlatBuffer payload).
- **Structural Mutation Tests:** Dispatches valid buffers, buffers with omitted optional fields, extreme Unicode strings, boundary numeric values (`0`, `-1`, `MaxUint64`), and truncated/corrupted frames, verifying that the actor never panics and cleanly drops malformed input or returns a typed error.
- **Domain Invariant Tests:** For each `INV-XX` extracted from `purpose.md`, the generator synthesizes targeted inputs.
  - Example (`INV-02`): Generates 100 `EnrichRequest` frames with `document_id` lengths of `0`, `13`, `15` and non-numeric characters, asserting 100% return `ErrorCode.INVALID_DOCUMENT` with latency `< 5ms`.
- **Idempotency Tests (`INV-04`):** Replays the exact same binary frame *N* times and asserts byte-for-byte identical response buffers.

### 6.2 Testing an Actor as Client (Emitter)

`boundary-mcp` spins up an **Oracle Server** (State-Machine Mock) speaking the `.fbs` contract:

1. The client actor is launched and pointed at the Oracle Server's socket.
2. The Oracle executes a battery of protocol scenarios derived from `state_machine.json`:
   - **Happy Path:** Accepts `EnrichRequest`, validates that outgoing fields satisfy `INV-01` and `INV-03`, and emits `EnrichResponse`.
   - **Domain Error Path:** Returns `EnrichError` and verifies the client transitions cleanly to a terminal state without infinite retry loops.
   - **Latency / Timeout Path (`INV-05`):** Deliberately holds the response for 600ms and counts retry attempts, failing the test if the client exceeds 2 retries or violates backoff bounds.
   - **Sequence Violation Path:** Injects an out-of-order frame or unknown `correlation_id` and verifies the client rejects it without corrupting internal state.

### 6.3 Strategy for External Actors (Legacy Systems / Third-Party APIs)

When a node in the topology represents an external service that does not natively speak FlatBuffers:

- `actors/actor_external_crm` houses strictly a thin **Boundary Adapter** (Anti-Corruption Layer).
- `boundary-mcp` verifies this adapter against a local HTTP/gRPC mock simulating real upstream payloads and fault modes, guaranteeing that translation into internal FlatBuffers is 100% deterministic and free of external schema leakage.

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

### Phase 1: Deterministic Core & CLI (`internal/fbs`)

- [x] Initialize Go module (`go mod init boundary-mcp`).
- [x] Implement wrapper around `flatc`:
  - [x] Code generation (`--go`, `--rust`).
  - [x] Schema evolution enforcement (`flatc --conform old.fbs new.fbs`).
- [x] Implement standard binary framing (`[4-byte length][FlatBuffer payload]`) for transport-agnostic IPC (Unix Socket / stdio / NATS).

### Phase 2: MCP Server & Sandbox (`internal/mcp` & `cmd/sandbox-run`)

- [x] Build Go MCP server (using `github.com/mark3labs/mcp-go`) exposing `get_boundary_context`, `propose_contract_evolution`, and `verify_actor_behavior`, over stdio and loopback Streamable HTTP.
- [x] Create `sandbox-run` wrapper with `bwrap` and an `opencode.json` template wired to the `boundary-mcp` binary, keeping governance outside the sandbox mount namespace.

### Phase 3: Bidirectional Semantic Compiler (`internal/compiler`)

- [ ] Integrate an LLM client (with strict JSON Schema structured outputs) inside `boundary-mcp` to translate `purpose.md` ↔ `protocol.fbs` + `state_machine.json`.
- [ ] Implement audit logging in `governance/history/` with human-readable diffs.

### Phase 4: Boundary Test Inducer (`internal/harness`)

- [ ] Implement property-based test generation using `pgregory.net/rapid` driven by `.fbs` binary schema reflection (`.bfbs` output from `flatc --binary --schema`).
- [ ] Implement the State-Machine Oracle Server to verify client actors.

### Phase 5: Litmus Test (Thesis Validation)

- [ ] Author a real `purpose.md` spanning 2 actors (`actor_ingest` in Go + `actor_enrich` in Rust or Go).
- [ ] Implement both actors in separate `sandbox-run` sessions.
- [ ] **Disposability Test:** Wipe `actors/actor_enrich/*` completely, launch a fresh `sandbox-run actor_enrich` session, and verify that the new agent reconstructs the actor from scratch and passes 100% of hidden boundary tests without touching `actor_ingest`.

---

### Implementation Note for Phase 4

One particularly useful feature of `flatc` for `internal/harness` is **`--binary --schema`**, which compiles any `.fbs` into a self-describing `.bfbs` (*Binary FlatBuffer Schema*). Using FlatBuffers' official reflection schema (`reflection.fbs`), the Go harness can **dynamically inspect tables, field types, enums, and default values at runtime** without recompiling `boundary-mcp` whenever a contract evolves — making the structured fuzzer and Oracle Server generic across any protocol you define.
