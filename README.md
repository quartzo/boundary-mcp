# boundary-mcp

> Purpose-driven development for actor-based distributed systems, backed by deterministic binary contracts.

**boundary-mcp** is a governance control plane that lets AI coding agents (such as [`opencode`](https://opencode.ai)) build isolated actors without leaking implementation details into each other. Agents work from a verbal purpose document and a compiled schema, while actors communicate at runtime exclusively through zero-copy FlatBuffers over IPC, Unix Domain Sockets, or NATS.

## Status

**Phase 3 complete.** `internal/compiler` implements the semantic compiler: an OpenAI-compatible LLM proposes complete `purpose.md`, `protocol.fbs` and `state_machine.json` contents, while `flatc` remains the gatekeeper for syntax and backward compatibility. Committed changes are written atomically and snapshotted under `governance/history/` with a unified diff and metadata. The next milestone is Phase 4 (deterministic harness). See the [roadmap](docs/ARCHITECTURE.md#8-implementation-roadmap-go-mvp).

## Why

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
    ├── harness/             # (planned) deterministic test runner
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
go test ./...    # run the test suite
go vet ./...
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

## Documentation

- [Architecture specification](docs/ARCHITECTURE.md) — topology, contracts, tool specs, isolation model, and roadmap.

## Roadmap

The MVP is organized in five phases, from the deterministic FlatBuffers core to a disposability litmus test. See [Implementation Roadmap](docs/ARCHITECTURE.md#8-implementation-roadmap-go-mvp).
