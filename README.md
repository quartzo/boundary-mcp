# boundary-mcp

> Purpose-driven development for actor-based distributed systems, backed by deterministic binary contracts.

**boundary-mcp** is a governance control plane that lets AI coding agents (such as [`opencode`](https://opencode.ai)) build isolated actors without leaking implementation details into each other. Agents work from a verbal purpose document and a compiled schema, while actors communicate at runtime exclusively through zero-copy FlatBuffers over IPC, Unix Domain Sockets, or NATS.

## Status

**Phase 1 complete.** The deterministic core lives in [`internal/fbs`](internal/fbs): a `flatc` wrapper (code generation, `.bfbs` reflection output, and a `--conform` compatibility gate) plus the `[uint32 LE length][FlatBuffer payload]` framing used between actors. The next milestone is Phase 2 (MCP server and sandbox launcher). See the [roadmap](docs/ARCHITECTURE.md#8-implementation-roadmap-go-mvp).

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
- a filtered boundary context served by `boundary-mcp` over stdio.

## Repository layout

The target layout (see the architecture document for details):

```text
.
├── docs/
│   └── ARCHITECTURE.md      # Design specification
├── governance/              # (planned) source of truth: purpose, schema, tests
├── sdk/                     # (planned) generated FlatBuffers bindings
├── actors/                  # (planned) one isolated sandbox per actor
├── cmd/
│   ├── boundary-mcp/        # (planned) MCP server
│   └── sandbox-run/         # (planned) bwrap/container launcher
└── internal/
    ├── compiler/            # (planned) purpose.md <-> protocol.fbs
    ├── fbs/                 # flatc wrapper, .bfbs reflection, framing
    ├── harness/             # (planned) deterministic test runner
    └── mcp/                 # (planned) MCP tool handlers
```

## Requirements

- **Go** 1.27 or newer
- **FlatBuffers compiler** (`flatc`)
- **Bubblewrap** (`bwrap`) on Linux, for sandboxed agent sessions
- **opencode**, for the agent-facing workflow

## Development

```bash
go build ./...   # build all packages
go test ./...    # run the test suite
```

The `internal/fbs` unit tests do not require `flatc` on the host: the compiler is exercised through an injected command runner. A real `flatc` is only needed for integration and generated bindings.

## Documentation

- [Architecture specification](docs/ARCHITECTURE.md) — topology, contracts, tool specs, isolation model, and roadmap.

## Roadmap

The MVP is organized in five phases, from the deterministic FlatBuffers core to a disposability litmus test. See [Implementation Roadmap](docs/ARCHITECTURE.md#8-implementation-roadmap-go-mvp).
