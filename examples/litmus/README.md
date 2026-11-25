# Litmus Example

A self-contained two-actor boundary used to validate the boundary-mcp thesis:

- `actor_ingest` (Go): emits `EnrichRequest` envelopes and retries after a timeout.
- `actor_enrich` (Go): validates requests and returns `EnrichResponse` or `EnrichError`.

The example deliberately keeps both actors in Go. The contract and suite format
are language-neutral; swapping `actor_enrich` for a Rust crate is a Phase 5
variant, not a requirement.

## Layout

```text
examples/litmus/
├── governance/
│   ├── purpose.md
│   ├── protocol.fbs
│   ├── state_machine.json
│   ├── aliases.json          # human names for content-addressed versions
│   ├── versions/<hash>/      # immutable snapshots
│   ├── actors/*.json         # adoption pins
│   ├── compat/*.json         # compatibility manifests
│   └── hidden_tests/
│       ├── actor_ingest.json
│       └── actor_enrich.json
├── actors/
│   ├── actor_ingest/       # nested Go module
│   └── actor_enrich/       # nested Go module
├── sdk/go/litmus/          # generated module, committed for reproducibility
├── verify/                 # runs the hidden suites with internal/harness
├── prompts/
│   └── reconstruct_actor_enrich.md
├── scripts/
│   └── disposability.sh
└── Makefile
```

## Run

From the repository root:

```bash
make litmus
```

Or from this directory:

```bash
make verify
make sdk      # regenerate sdk/go from governance/protocol.fbs
```

`make verify` builds each actor inside its workspace and runs the hidden suite:

- `actor_enrich` in **server** mode: valid seeds must produce a valid
  `Envelope`, and structural mutations must be dropped or answered without
  closing the connection.
- `actor_ingest` in **client** mode: scripted scenarios check the request
  fields, exercise the happy path and force a timeout so the actor must retry.

## Versioning

The example governance is snapshotted as `v0` in
`governance/versions/<hash>/` and both actors are pinned to it. The snapshot is
immutable; editing `purpose.md`, `protocol.fbs` or `state_machine.json` creates a
new candidate without changing `v0`.

From the repository root:

```bash
go run ./cmd/versionctl list -governance examples/litmus/governance
go run ./cmd/versionctl compat -governance examples/litmus/governance -base v0
```

`compat` compares the working tree with `v0` and writes a manifest under
`governance/compat/`. See
[ADR-0002](../../docs/adr/0002-content-addressed-protocol-versions.md).

## Phase split

- **5a (this example):** no LLM required. Contract, actors, suites and
  deterministic verification.
- **5b (manual thesis run):** wipe `actors/actor_enrich/`, start
  `sandbox-run` with the prompt in `prompts/reconstruct_actor_enrich.md`, and
  verify the reconstructed actor. This step needs an `opencode` model.

```bash
CONFIRM=1 ./examples/litmus/scripts/disposability.sh
```

The script backs up the current actor, wipes it, launches the sandbox, runs
the suites and restores the backup if the reconstruction fails. On success it
keeps the reconstructed implementation for review.

## Phase 5b result

On 2026-11-24, `actor_enrich` was wiped and reconstructed by a sandboxed
`opencode` session working only from the boundary context (`purpose.md`,
`protocol.fbs` via the MCP server, and the read-only `/sdk` module). The session
could not read `actor_ingest` or the hidden suites.

- `actor_enrich`: **PASS**, 11 hidden cases.
- `actor_ingest`: **PASS**, 3 scripted cases.
- Reconstructed implementation: `actors/actor_enrich/main.go` (committed).
- Model reported by the sandboxed session: `radixark/daisyrose-27b`.

The agent replaced the original framing helper with a single self-contained
`main.go`, used only the generated SDK, the FlatBuffers runtime and the
standard library, and passed its own smoke tests before the harness ran the
hidden suite from outside the session.

Verification is no longer an MCP tool. `make litmus` runs
`examples/litmus/verify`, which builds each actor and executes the behavioral
suites directly; CI can run the same command.

## Modules

- `sdk/go/litmus` is a Go module with module path `litmus`.
- Each actor is a nested module with
  `replace litmus => ../../sdk/go/litmus`. The same relative path resolves to
  `/sdk/go/litmus` when the actor workspace is mounted at `/workspace`.
- `sandbox-run` binds the host Go module and build caches so the agent can
  build without re-downloading dependencies.

## Notes

- The verification subprocesses currently run on the host. Sandboxing them is
  tracked as a hardening item in the architecture roadmap.
- The generated Go bindings are committed so the example works without
  `flatc`. Run `make sdk` after changing `protocol.fbs`.
- `verify` imports `internal/harness` because it lives in the same Go module.
  Actors import only the generated SDK (`litmus`) and the standard library.
