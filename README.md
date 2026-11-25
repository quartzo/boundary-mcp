# boundary-mcp

> Purpose-driven development for actor-based distributed systems, backed by deterministic binary contracts.

**boundary-mcp** is a governance control plane that lets AI coding agents (such as [`opencode`](https://opencode.ai)) build actors without leaking implementation details into each other. Agents work from a verbal purpose document and a compiled schema, while actors communicate at runtime exclusively through zero-copy FlatBuffers over IPC, Unix Domain Sockets, or NATS.

## Status

**Semantic engine enforced; litmus and versioning green.** The semantic engine (`internal/judge`) is the protocol-management core: it calls JEV through OpenRouter's typed System One API to decide whether a proposal is the right protocol, while `flatc` decides whether that decision is expressible and wire-compatible. The gate is mandatory whenever the semantic compiler is enabled and fails closed. A 100-case synthetic calibration set the default review threshold to 0.40, and `make judge-e2e` exercises the live compiler-to-gate path. The Phase 5 litmus remains green. The control plane manages contracts and publishes protocol tests; the coder and CI are external ([ADR-0004](docs/adr/0004-protocol-only-mcp.md)). The content-addressed versioning prototype (`versionctl`) snapshots the litmus contract as `v0`, classifies compatibility with `flatc --conform` plus a reflection diff, and records actor adoption. See [ADR-0003](docs/adr/0003-semantic-engine-as-protocol-management-core.md) for the layer model, plus [ADR-0001](docs/adr/0001-advisory-semantic-judge.md), [ADR-0002](docs/adr/0002-content-addressed-protocol-versions.md) and the [roadmap](docs/ARCHITECTURE.md#8-implementation-roadmap-go-mvp).

## Why Boundary-MCP Exists: The Illusion of JSON Flexibility

Modern distributed systems overwhelmingly default to **JSON** as the wire protocol between services and agents. The standard justification is *evolutionary flexibility*—the ability to add fields and iterate quickly without upfront schema coordination.

In practice, this flexibility is an accounting illusion. Schemaless protocols do not eliminate contract rigidity; they merely move it out of the compiler and into **fragile mental models** (and now, into the stochastic context windows of LLMs).

### The Hidden Cost of Mental-Model Contracts

1. **Semantics Trapped in Tribal Knowledge:** A JSON payload like `{"status": "pending"}` carries structure, not semantics. It does not specify idempotency guarantees, retry bounds, nullability semantics, or valid state transitions. Those rules live implicitly inside imperative code paths and developers' heads—until an AI agent refactors a module and silently breaks an unstated invariant.
2. **Inversion of Engineering Gravity:** Because keeping prose documentation, IDLs, and code synchronized manually is tedious, teams treat imperative implementation as the sole source of truth and the boundary protocol as an accidental byproduct. When LLMs operate under this model, they optimize for making local code pass by quietly mutating the boundary—eroding system modularity into a distributed monolith.
3. **Evolution Without Rigor Is Unnecessary:** Binary formats like **FlatBuffers** solved schema evolution over a decade ago. You can append fields, deprecate obsolete ones, and maintain forward/backward compatibility (`flatc --conform`) while gaining *zero-copy* performance and compile-time type safety.

### Shifting the Center of Gravity

**Boundary-MCP** inverts the workflow:

- **Human and AI cognitive effort is invested at the boundary**, co-evolving human-readable intent (`purpose.md`) with a strict, evolvable binary contract (`protocol.fbs`) and executable behavioral invariants.
- **Internal actor implementations become disposable commodities**, developed in their own service directory with zero visibility into neighboring internals—guaranteed to be interchangeable as long as they satisfy the boundary contract.

## Failure Modes Addressed

LLM-assisted software engineering tends to fail in three predictable ways:

1. **Abstraction leaks** — agents that can read neighboring source code create implicit couplings.
2. **Semantic contract drift** — agents loosen schemas or change signatures to make local tests pass.
3. **Runtime inference tax** — verbal protocols on the hot path (MCP/A2A) add latency, cost, and non-determinism.

boundary-mcp addresses each of them:

- **Design plane vs. data plane** — the semantic engine and its gates operate only at design/governance time. Runtime is pure binary.
- **Capability-based isolation** — the coder runs in the actor's own service directory; boundary-mcp never receives actor code and only exposes that actor's boundary view.
- **Bidirectional contracts** — `purpose.md` and `protocol.fbs` co-evolve behind deterministic gates (`flatc --conform`).
- **Semantic gate** — whether a change is the *right* protocol (intent fit, scope, invariants, drift) is decided by a calibrated engine, not left to the implementing agent's interpretation.
- **Black-box verification** — property-based and state-machine suites run against actor binaries in CI, with minimal counterexamples returned to the coder.

## Architecture at a glance

```text
        DESIGN PLANE (subjective + deterministic)          DATA PLANE (runtime)

  intent --> semantic engine (JEV) --> purpose.md + protocol.fbs --> [flatc] --> SDK
                    |                              |
                    |                              +--> [harness] --> actor suite (CI)
                    |
                    +--> "is this the right protocol?"

  [flatc]   "is it expressible and compatible?"
  [harness] "does the actor obey it?"

  Actor A ==[FlatBuffers]==> Actor B
```

The semantic engine decides *whether a change is legitimate*. `flatc`, the harness and the version store make that decision expressible, honored and attributable. They are not an appendix to a deterministic core; they are the gates and the ledger the engine drives. See [ADR-0003](docs/adr/0003-semantic-engine-as-protocol-management-core.md).

Each coder session sees only:

- its own service directory (read-write),
- the generated SDK, mounted read-only by the project,
- a filtered boundary context served by `boundary-mcp` over MCP.

## Workflows

**Legend:** **Current** is implemented today. **Designed** is the accepted direction; it is not wired yet. The full reference is [docs/WORKFLOWS.md](docs/WORKFLOWS.md).

### Actor loop (Current)

```text
coder in actors/actor_x/ (user-owned)
        |
        v
boundary-mcp (MCP over stdio or HTTP)
        |
        +--> get_boundary_context(actor_x)
        |       filtered purpose + active protocol + read-only SDK
        |
        +--> agent implements in its service directory
        |
        +--> agent runs the protocol test spec locally
        |       boundary://protocol/{hash}/tests
        |
        +--> CI builds the actor and runs its behavioral suite
                PASS or minimal counterexample
```

The coder sees its own service directory; `boundary-mcp` exposes only that actor's boundary view and never receives the code. The litmus example keeps a reproducible sandboxed runner for the Phase 5 disposability experiment; see [examples/litmus/README.md](examples/litmus/README.md).

### Evolution loop (Current)

```text
propose_contract_evolution(actor, intent, ...)
        |
        v
LLM produces complete purpose.md + protocol.fbs + state_machine.json
        |
        v
flatc --binary --schema      syntax/types        fail -> rejected
        |
        v
flatc --conform base next   wire compatibility  fail -> rejected
        |
        v
SDK dry-run                  Go/Rust bindings     fail -> rejected
        |
        v
semantic gate (JEV)          is this legitimate? reject -> rejected
        |                                        review -> escalated
        v
atomic commit + history snapshot + SDK regeneration
        |
        v
actor runs the protocol tests; CI runs the behavioral suite
```

`-sync` is the administrative equivalent for manual edits to `purpose.md` or `protocol.fbs`. It detects the changed artifact, updates the counterpart with the LLM, and runs the same gates. If both artifacts changed, it refuses to guess.

This is a **single-line, auto-commit** model:

- a passing proposal becomes active immediately;
- every actor on the active line is bound by it;
- there is no draft, candidate, promotion or abandonment state yet;
- history records commits, not rejected proposals.

The semantic gate fails closed. If the semantic compiler is enabled, a missing OpenRouter key stops startup; there is no supported operating mode without the gate.

### Versioned evolution loop (Designed)

```text
snapshot active as vN (content hash + alias)
        |
        v
proposal -> draft_id + candidate_hash        (no active change)
        |
        v
compatibility manifest
  flatc --conform       wire compatibility
  reflection diff       field/enum/root changes
  JEV classifier        semantic + per-edge impact
        |
        +--> additive / producer_only / consumer_only
        |         -> verify actors against the draft
        |         -> promote -> active vN+1 + alias + wire version
        |
        +--> semantic_change
        |         -> human review -> revise, promote or abandon
        |
        +--> breaking
        |         -> major line v2 + dual-run + migration window
        |
        +--> abandoned
                  -> status=abandoned, reason recorded, no version bump
```

Draft states:

```text
draft -> candidate -> active -> deprecated
  \-> abandoned
```

A drafted change does not affect the active contract. Actors can be verified against the draft without adopting it. Promotion requires the gates, the required actor upgrades and owner approval; a breaking change cannot be promoted on the active line.

### Compatibility classes

| Class | Meaning | Upgrade order |
| --- | --- | --- |
| `identical` | No structural change. | Any |
| `additive` | Old peers can ignore the change. | Any |
| `producer_only` | Only the sender must upgrade. | Producer first |
| `consumer_only` | Only the receiver must upgrade. | Consumer first |
| `requires_peer_upgrade` | The other side must understand the change. | Coordinated |
| `semantic_change` | Wire-compatible, but behavior/invariants changed. | Review required |
| `breaking` | Old and new cannot interoperate. | Dual-run / major line |
| `unknown` | Needs human review. | Blocked |

`flatc --conform` is authoritative for wire compatibility, but it is not sufficient. It accepts adding a `required` field to an existing table; the reflection diff marks that as breaking because old peers do not send it.

### Current vs designed

| Capability | Status |
| --- | --- |
| Actor context, protocol test resources and reference behavioral verification | Current |
| `propose_contract_evolution` auto-commit | Current |
| Manual `-sync` reconciliation | Current |
| Semantic gate (JEV) enforced with the semantic compiler | Current |
| History snapshots and diffs | Current |
| Content-addressed snapshots, aliases, adoption pins | Current |
| `flatc --conform` + reflection compatibility manifest | Current |
| Draft → candidate → active lifecycle | Designed |
| Promote / abandon operations | Designed |
| JEV role-specific compatibility (`producer_only`, `consumer_only`, `requires_peer_upgrade`) | Designed |
| Pairwise adoption matrix and promotion policy | Designed |
| `protocol_version:uint` in the envelope | Designed |
| Breaking-change major line and dual-run | Designed |
| Append-only event log for alias/promotion/adoption events | Designed |

### Essential commands

```bash
# Control plane (stdio for a local coder; HTTP for a remote one)
./bin/boundary-mcp -governance governance
./bin/boundary-mcp -transport http -addr 127.0.0.1:8765 -token "$BOUNDARY_MCP_TOKEN" -governance governance

# Evolution (single-line, current)
./bin/boundary-mcp -llm-model ...
./bin/boundary-mcp -sync -governance governance

# Version metadata (current prototype)
make version-snapshot
make version-compat
make version-list

go run ./cmd/versionctl snapshot -governance governance -alias v1
go run ./cmd/versionctl compat -governance governance -base v1
go run ./cmd/versionctl pin -governance governance -actor actor_ingest -role producer -version v1
go run ./cmd/versionctl list -governance governance
```

`make version-*` targets default to `examples/litmus/governance`; override with `VERSION_GOVERNANCE=...`.

See [docs/WORKFLOWS.md](docs/WORKFLOWS.md) for the state machines, promotion gate, breaking-change procedure, field/enum rules and failure paths.

## Repository layout

The target layout (see the architecture document for details):

```text
.
├── docs/
│   └── ARCHITECTURE.md      # Design specification
├── examples/
│   └── litmus/              # two-actor acceptance example
│       └── harness/         # reference behavioral verifier (example/CI only)
├── governance/              # (planned) source of truth: purpose, schema, tests
├── sdk/                     # (planned) generated FlatBuffers bindings
├── actors/                  # (planned) one service directory per actor (user-owned)
├── bin/                     # build output (gitignored)
├── cmd/
│   ├── boundary-mcp/        # MCP server (stdio / loopback HTTP)
│   ├── judge-eval/          # labeled proposal benchmark for JEV
│   └── versionctl/          # content-addressed versions, aliases, adoption
└── internal/
    ├── compiler/            # LLM semantic compiler, flatc gates, history
    ├── fbs/                 # flatc wrapper
    ├── governance/          # purpose.md parser and actor filtering
    ├── judge/               # semantic gate: JEV / System One judgments
    ├── llm/                 # OpenAI-compatible structured-output client
    ├── mcp/                 # MCP tools, resources, handlers, HTTP auth
    ├── protocol/            # .bfbs reflection, frame builder, validation, mutations
    ├── protocoltest/        # declarative protocol test spec and expansion
    └── versioning/          # version hashes, aliases, compatibility manifests
```

## Requirements

- **Go** 1.27 or newer
- **FlatBuffers compiler** (`flatc`) — exercised in integration tests with 25.12.x
- **Bubblewrap** (`bwrap`) on Linux, only for the litmus example runner
- A **coder with MCP support** (for example [`opencode`](https://opencode.ai))
- An **OpenAI-compatible LLM endpoint** (optional), for the semantic compiler
- An **OpenRouter API key** (required when the semantic compiler is enabled), for the semantic gate (JEV)

## Development

```bash
make build       # build bin/boundary-mcp
make test        # run the test suite
make litmus      # regenerate the example SDK and run the acceptance example
```

Unit tests use an injected command runner and do not require `flatc`. The integration tests in `internal/fbs` exercise a real `flatc` and run automatically when it is on `PATH`; they are skipped otherwise.

## Connecting a coder

Start the control plane and point your coder's MCP configuration at it. The coder owns the actor's service directory; boundary-mcp never receives its code.

```bash
make build
export BOUNDARY_MCP_TOKEN=$(openssl rand -hex 24)
./bin/boundary-mcp -transport http -addr 127.0.0.1:8765 \
  -actor-token "actor_ingest=$BOUNDARY_MCP_TOKEN" -governance governance
```

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "boundary": {
        "type": "remote",
        "url": "http://127.0.0.1:8765/mcp",
        "oauth": false,
        "headers": { "Authorization": "Bearer {env:BOUNDARY_MCP_TOKEN}" }
      }
    }
  },
  "permissions": [
    { "action": "external_directory", "resource": "*", "effect": "deny" }
  ]
}
```

`-actor-token actor=token` binds the bearer to one actor; requests for another actor are rejected. `-token` is an unscoped token for local or administrative use. `external_directory: deny` keeps the coder inside the actor's directory. That rule is the isolation boundary, and it belongs to the coder's configuration, not to the server. The litmus example ships a reproducible sandboxed runner for the disposability experiment; see [examples/litmus/README.md](examples/litmus/README.md).

## Semantic compiler

`propose_contract_evolution` and the one-shot `-sync` mode are enabled when an OpenAI-compatible endpoint is configured:

```bash
export BOUNDARY_LLM_BASE_URL=https://openrouter.ai/api/v1
export BOUNDARY_LLM_API_KEY=...
export BOUNDARY_LLM_MODEL=...

make build
./bin/boundary-mcp -sync -governance governance
```

The semantic engine decides whether the proposal is the right protocol; `flatc --binary --schema` and `flatc --conform` decide whether it is valid and compatible. A change commits only when both agree. Committed changes are written atomically and snapshotted under `governance/history/<timestamp>_<actor>/` with `diff.patch` and `meta.json`. Without an LLM configured, the tool reports that the compiler is unavailable.

## Semantic engine (protocol management)

The semantic engine runs after `flatc` and before commit. It answers the question `flatc` cannot: is this the right protocol?

```bash
export OPENROUTER_API_KEY=...
./bin/boundary-mcp -llm-model ...
```

It calls JEV through OpenRouter's typed System One API (`POST /api/v1/systemone`, pinned `typesafe/jev-1.14`). It is authoritative for intent fit, scope, invariants and semantic drift; `flatc` remains authoritative for syntax and wire compatibility, and the harness for behavior. None of them can overrule another's domain. The gate is mandatory whenever the semantic compiler is enabled: startup fails if no OpenRouter key resolves. There is no product off switch; unit tests use a fake judge. For local runs, when `OPENROUTER_API_KEY` is unset, the key is read from opencode's auth store (`~/.local/share/opencode/auth.json`); production deployments should set the environment variable.

The calibrated defaults are `-judge-review-at 0.40` and `-judge-reject-at 0.70`. Run the labeled benchmark with:

```bash
go run ./cmd/judge-eval -cases internal/judge/testdata/cases
make judge-calibrate   # built-in 100-case synthetic set
make judge-e2e         # one live compiler -> gate proposal
```

A 100-case synthetic calibration at 0.40/0.70 produced 0 false allows across two runs; false escalations on compatible cases were 0 in the first run and 1 in the second (allow maximum 0.39–0.42), while semantic violations started at 0.89. p95 was 356–558 ms and the run cost ≈ $0.0055 (≈ $0.00005 per case). JEV probabilities vary between runs, so the verdict is only as stable as its binding to the content hash (ADR-0003 follow-up).

See [ADR-0001](docs/adr/0001-advisory-semantic-judge.md) for the gate decision and [ADR-0003](docs/adr/0003-semantic-engine-as-protocol-management-core.md) for the layer model.

## Protocol tests

Protocol tests are published, not executed. The server validates every spec under `governance/protocol_tests/*.json` and exposes them for the active content hash:

- `boundary://protocol/{hash}/fbs` — `protocol.fbs` and, when present, the compiled `.bfbs`
- `boundary://protocol/{hash}/tests` — the normative specs
- `boundary://protocol/{hash}/purpose` — the full `purpose.md`
- `boundary://actor/{name}/context` — the filtered boundary view for one actor

`get_protocol_tests` is the tool fallback for clients that do not read resources. A spec is declarative: `seeds` provide valid frames, `rules` carry explicit valid/invalid examples for semantic assertions, and `mutations` select structural cases. `internal/protocoltest` expands a spec deterministically (sorted cases, base64 frames) for the coder or CI to run. The protocol-test path never builds or runs actor code.

## Protocol versions and compatibility

Version identity is content-addressed. A snapshot hashes `purpose.md`, `protocol.fbs` and `state_machine.json`; the hash is the identity, while the date, actor and intent are annotations. A separate wire hash identifies `protocol.fbs` alone. Aliases such as `v0` are human pointers to immutable hashes.

`versionctl` is the first prototype for the versioning model; it snapshots and analyzes without changing the active contract:

```bash
go run ./cmd/versionctl snapshot -governance examples/litmus/governance -alias v0
go run ./cmd/versionctl compat -governance examples/litmus/governance -base v0
go run ./cmd/versionctl pin -governance examples/litmus/governance -actor actor_ingest -role producer -version v0
go run ./cmd/versionctl list -governance examples/litmus/governance
```

`compat` runs `flatc --conform` and a reflection diff. The manifest records wire compatibility, structural changes, a class (`identical`, `additive`, `semantic_change`, `breaking`) and whether human review is required. Role-specific classes (`producer_only`, `consumer_only`, `requires_peer_upgrade`) are the next step for the JEV classifier.

Actor pins record adoption without changing the contract. The prototype does not change the compiler's auto-commit lifecycle; draft/promote/abandon and the pairwise adoption matrix are follow-ups. The litmus example includes its `v0` snapshot and actor pins under `examples/litmus/governance/`. See [ADR-0002](docs/adr/0002-content-addressed-protocol-versions.md).

## Reference behavioral verifier

`examples/litmus/harness` is the reference behavioral verifier used by the litmus example and by CI. It is no longer an MCP tool. It builds on `internal/protocol` for schema reflection, validation and frame building, and verifies actor binaries without reading their source:

- `protocol.LoadSchema` parses the `.bfbs` emitted by `flatc --binary --schema`.
- `Schema.Validate` and `BoundaryMutations` check frames and generate structural mutations (boundary scalars, invalid UTF-8, truncation, corrupted offsets).
- `HarnessClient` verifies actors that receive messages: valid seeds must respond, malformed frames may be dropped or answered with a valid buffer, and a closed connection is a failure.
- `ReferenceMockPeer` verifies actors that emit messages with scripted request/response scenarios, checks, delays and timeout/sequence paths.
- A dynamic builder turns `.bfbs` metadata plus field values into valid FlatBuffers, so suites never carry base64 payloads.

Property tests for the protocol core use `pgregory.net/rapid`. `examples/litmus/verify` loads a trusted suite per actor:

```json
{
  "mode": "server",
  "schema": "protocol.bfbs",
  "response_table": "EnrichResponse",
  "build": "go build -o bin/actor ./...",
  "run": ["./bin/actor", "--socket", "{socket}"],
  "seeds": [
    {"table": "EnrichRequest", "values": {"document_id": "12345678901234"}}
  ]
}
```

`mode: "client"` instead runs the Reference Mock Peer with `scenarios` and declarative checks (`regex`, `min`, `max`, `equals`). The reference verifier resolves actor workspaces and suites through `harness.VerifierOptions`. `build` and `run` are trusted suite values; pin `build` there. Verification subprocesses still run on the host — sandboxing them is the next hardening step.

## Documentation

- [Architecture specification](docs/ARCHITECTURE.md) — topology, layer model, tool and resource specs, environment and roadmap.
- [Workflows](docs/WORKFLOWS.md) — actor loop, auto-commit evolution, versioned draft lifecycle, compatibility and breaking changes.
- [Plan: protocol-only boundary-mcp](docs/PLAN.md) — the re-scoping record and staged work.
- [Litmus example](examples/litmus/README.md) — two-actor acceptance fixture and hidden suites.
- [ADR-0004: Protocol Manager, Not an Agent Runtime or CI](docs/adr/0004-protocol-only-mcp.md) — target scope and the external-coder model.
- [ADR-0003: Semantic Engine as Protocol-Management Core](docs/adr/0003-semantic-engine-as-protocol-management-core.md) — the four layers and their authority split.
- [ADR-0001: Advisory Semantic Judge](docs/adr/0001-advisory-semantic-judge.md) — JEV gate decision; title retained as the historical record.
- [ADR-0002: Content-Addressed Protocol Versions](docs/adr/0002-content-addressed-protocol-versions.md) — version hashes, compatibility classes, aliases and actor adoption.
- [ADR index](docs/adr/README.md) — conventions and decision history.

## Roadmap

The phase numbering records **construction order, not architecture hierarchy**. The semantic engine is the protocol-management core from the moment the semantic compiler is enabled (ADR-0003); the deterministic layers are the gates and the ledger it drives, and the ADR-0002 versioning prototype is its identity layer. See [Implementation Roadmap](docs/ARCHITECTURE.md#8-implementation-roadmap-go-mvp).
