# boundary-mcp

> Purpose-driven development for actor-based distributed systems, backed by deterministic binary contracts.

**boundary-mcp** is a governance control plane that lets AI coding agents (such as [`opencode`](https://opencode.ai)) build isolated actors without leaking implementation details into each other. Agents work from a verbal purpose document and a compiled schema, while actors communicate at runtime exclusively through zero-copy FlatBuffers over IPC, Unix Domain Sockets, or NATS.

## Status

**Phase 5b complete for `actor_enrich`.** `examples/litmus/` holds the two-actor boundary, contract, hidden suites and `make litmus`. The `actor_enrich` implementation was wiped and reconstructed by a sandboxed `opencode` session using only the boundary context (purpose, protocol and read-only SDK); the reconstructed actor passes the hidden suite. Repeating the reconstruction for `actor_ingest` remains optional. See the [roadmap](docs/ARCHITECTURE.md#8-implementation-roadmap-go-mvp).

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
- **Internal actor implementations become disposable commodities**, developed in isolated sandboxes with zero visibility into neighboring internals—guaranteed to be interchangeable as long as they satisfy the boundary contract.

## Failure Modes Addressed

LLM-assisted software engineering tends to fail in three predictable ways:

1. **Abstraction leaks** — agents that can read neighboring source code create implicit couplings.
2. **Semantic contract drift** — agents loosen schemas or change signatures to make local tests pass.
3. **Runtime inference tax** — verbal protocols on the hot path (MCP/A2A) add latency, cost, and non-determinism.

boundary-mcp addresses each of them:

- **Design plane vs. data plane** — LLMs are used only at design/governance time. Runtime is pure binary.
- **Capability-based isolation** — each actor is developed in a `bwrap`/container sandbox that cannot see other actors and has read-only access to SDK bindings.
- **Bidirectional contracts** — `purpose.md` and `protocol.fbs` co-evolve behind deterministic gates (`flatc --conform`).
- **Black-box verification** — hidden property-based and state-machine tests run against actor binaries, with only minimal counterexamples returned to the agent.

## Architecture at a glance

```text
        DESIGN PLANE (LLM + governance)          DATA PLANE (runtime)
  purpose.md <--> protocol.fbs --[flatc]--> SDK   Actor A ==[FlatBuffers]==> Actor B
        |                    |
        +----[hidden tests]--+--> verify_actor_behavior
```

Each agent session sees only:

- its own `/workspace` (read-write),
- the generated SDK under `/sdk` (read-only),
- a filtered boundary context served by `boundary-mcp` over a bearer-protected loopback endpoint.

## Repository layout

The target layout (see the architecture document for details):

```text
.
├── docs/
│   └── ARCHITECTURE.md      # Design specification
├── examples/
│   └── litmus/              # two-actor acceptance example and hidden suites
├── governance/              # (planned) source of truth: purpose, schema, tests
├── sdk/                     # (planned) generated FlatBuffers bindings
├── actors/                  # (planned) one isolated sandbox per actor
├── bin/                     # build output (gitignored)
├── cmd/
│   ├── boundary-mcp/        # MCP server (stdio / loopback HTTP)
│   └── sandbox-run/         # bwrap launcher + opencode.json template
└── internal/
    ├── compiler/            # LLM semantic compiler, flatc gates, history
    ├── fbs/                 # flatc wrapper, .bfbs reflection, framing
    ├── governance/          # purpose.md parser and actor filtering
    ├── harness/             # .bfbs reflection, frame builder, verifier engines
    ├── llm/                 # OpenAI-compatible structured-output client
    └── mcp/                 # MCP tools, handlers, HTTP auth
```

## Requirements

- **Go** 1.27 or newer
- **FlatBuffers compiler** (`flatc`) — exercised in integration tests with 25.12.x
- **Bubblewrap** (`bwrap`) on Linux, for sandboxed agent sessions
- **opencode**, for the agent-facing workflow
- An **OpenAI-compatible LLM endpoint** (optional), for the semantic compiler

## Development

```bash
make build       # build bin/boundary-mcp
make test        # run the test suite
make litmus      # regenerate the example SDK and run the acceptance example
```

Unit tests use an injected command runner and do not require `flatc`. The integration tests in `internal/fbs` exercise a real `flatc` and run automatically when it is on `PATH`; they are skipped otherwise.

## Sandbox

Once `actors/<name>/` and `governance/` exist, launch an isolated session with:

```bash
./cmd/sandbox-run/sandbox-run.sh actor_ingest
```

`sandbox-run` starts `boundary-mcp` outside the sandbox, waits for readiness, renders `opencode.json`, and launches `bwrap` with only the actor workspace and the read-only SDK mounted. Set `BOUNDARY_DRY_RUN=1` to inspect the plan without launching anything.

## Semantic compiler

`propose_contract_evolution` and the one-shot `-sync` mode are enabled when an OpenAI-compatible endpoint is configured:

```bash
export BOUNDARY_LLM_BASE_URL=https://openrouter.ai/api/v1
export BOUNDARY_LLM_API_KEY=...
export BOUNDARY_LLM_MODEL=...

make build
./bin/boundary-mcp -sync -governance governance
```

The LLM may only propose; `flatc --binary --schema` and `flatc --conform` decide whether a change is valid. Committed changes are written atomically and snapshotted under `governance/history/<timestamp>_<actor>/` with `diff.patch` and `meta.json`. Without an LLM configured, the tool reports that the compiler is unavailable.

## Harness

`internal/harness` verifies actor binaries without reading their source:

- `LoadSchema` parses the `.bfbs` emitted by `flatc --binary --schema`.
- `Schema.Validate` and `BoundaryMutations` check frames and generate structural mutations (boundary scalars, invalid UTF-8, truncation, corrupted offsets).
- `HarnessClient` verifies actors that receive messages: valid seeds must respond, malformed frames may be dropped or answered with a valid buffer, and a closed connection is a failure.
- `ReferenceMockPeer` verifies actors that emit messages with scripted request/response scenarios, checks, delays and timeout/sequence paths.
- A dynamic builder turns `.bfbs` metadata plus field values into valid FlatBuffers, so suites never carry base64 payloads.

Property tests in the harness use `pgregory.net/rapid`. `verify_actor_behavior` loads a trusted suite per actor:

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

`mode: "client"` instead runs the Reference Mock Peer with `scenarios` and declarative checks (`regex`, `min`, `max`, `equals`). The verifier resolves actor workspaces with `-actors` (default `actors`) and suites with `-hidden-tests` (default `<governance>/hidden_tests`). `build` and `run` are trusted suite values; pin `build` there instead of relying on the agent-provided `entrypoint_cmd`. Verification subprocesses still run on the host — sandboxing them is the next hardening step.

## Documentation

- [Architecture specification](docs/ARCHITECTURE.md) — topology, contracts, tool specs, isolation model, and roadmap.
- [Litmus example](examples/litmus/README.md) — two-actor acceptance fixture and hidden suites.
- [ADR-0001: Advisory Semantic Judge](docs/adr/0001-advisory-semantic-judge.md) — proposed JEV / System One layer; not part of the current design.
- [ADR index](docs/adr/README.md) — conventions and decision history.

## Roadmap

The MVP is organized in five phases, from the deterministic FlatBuffers core to a disposability litmus test. See [Implementation Roadmap](docs/ARCHITECTURE.md#8-implementation-roadmap-go-mvp).
