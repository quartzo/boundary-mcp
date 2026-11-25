# Protocol Evolution Workflows

This document describes the workflows boundary-mcp is designed around. It separates what is implemented today from the target lifecycle, so the documentation does not imply that a designed step already runs.

Protocol management is a subjective decision: whether a change is the **right** protocol. The workflows below separate that decision — owned by the semantic engine — from the deterministic layers that make it expressible, compatible, honored and attributable. See [ADR-0003](adr/0003-semantic-engine-as-protocol-management-core.md).

**Legend**

- **Current** — implemented in this repository and covered by tests.
- **Designed** — accepted direction or roadmap item; the interface or lifecycle is not wired yet.

## 1. Roles and gates

| Role | Responsibility |
| --- | --- |
| Actor agent | Implements one actor from its boundary context, proposes contract changes, and verifies against hidden suites. |
| Architect / owner | Owns governance, approves promotions and aliases, and authorizes a new major line for a breaking change. |
| `boundary-mcp` | The only agent-facing write path for the contract. Runs the compiler, gates, history, SDK regeneration and version metadata. |
| `flatc` | Deterministic syntax/type gate and `--conform` wire-compatibility gate. |
| Semantic engine (JEV) | The protocol-management core. Decides whether a change is the right protocol: intent fit, scope, invariants, drift and (designed) role-specific impact. It never decides wire compatibility. |
| Harness | Black-box behavioral verification against hidden suites. |

Each layer is authoritative inside its own domain. The semantic engine decides legitimacy and can reject or escalate; `flatc` decides conformance and cannot be overruled by a semantic verdict; the harness decides behavior; versioning decides identity. None of them can overrule another's domain.

## 2. Actor development (Current)

```text
sandbox-run actor_x
        |
        v
boundary-mcp running outside the sandbox
        |
        +--> get_boundary_context(actor_x)
        |       - purpose.md filtered to actor_x
        |       - active protocol.fbs
        |       - read-only SDK path
        |
        +--> agent implements in /workspace
        |
        +--> verify_actor_behavior(actor_x)
                - trusted hidden suite
                - PASS, or a minimal counterexample
```

The actor sandbox contains only:

- its own read-write `/workspace`;
- the generated SDK mounted read-only;
- the bearer-protected MCP endpoint.

It does **not** contain `governance/`, other actors, or the hidden tests. The agent cannot change the contract from the sandbox; it can only request a change through the evolution workflow.

## 3. Proposal and auto-commit (Current)

```text
propose_contract_evolution(actor, intent, snippet?, affected_invariants)
        |
        v
load current purpose.md + protocol.fbs + state_machine.json
        |
        v
LLM structured output: complete candidate artifacts
        |
        v
flatc --binary --schema          syntax/types        fail -> rejected
        |
        v
flatc --conform base candidate   wire compatibility  fail -> rejected
        |
        v
SDK dry-run (Go/Rust)            bindings still build fail -> rejected
        |
        v
semantic gate (JEV)              mandatory           reject -> rejected
        |                                            review -> escalated
        v
atomic commit
  - purpose.md, protocol.fbs, state_machine.json
  - history/<timestamp>_<actor>/ with meta.json + diff.patch
  - regenerate SDKs
  - OnCommit hook (stale marking placeholders)
        |
        v
actor calls verify_actor_behavior(actor)
```

`propose_contract_evolution` is the only agent-facing write path. The semantic engine decides whether the proposal is the right protocol; `flatc` decides whether it is valid and compatible. The change commits only when both agree.

The current model is **single-line and auto-commit**:

- a proposal that passes all gates is written to the active files immediately;
- there is no draft, candidate or promotion step;
- the accepted change is binding for every actor on the active line;
- history records committed changes, not rejected or abandoned proposals.

Result statuses:

| Status | Meaning |
| --- | --- |
| `committed` | All gates passed and the active artifacts were replaced. |
| `rejected` | Syntax, wire compatibility, SDK dry-run or the semantic gate rejected it. Diagnostics are returned. |
| `escalated` | The semantic gate is in the review band; the change is not committed. |
| `unchanged` | The candidate is identical to the current artifacts. |

## 4. Manual edit sync (Current)

The human architect can edit `purpose.md` or `protocol.fbs` directly. `-sync` is the administrative reconciliation path:

```text
human edits purpose.md OR protocol.fbs
        |
        v
boundary-mcp -sync
        |
        v
detect which artifact changed (latest history snapshot, then mtime)
        |
        +--> purpose.md changed  -> LLM updates protocol.fbs + state_machine.json
        |
        +--> protocol.fbs changed -> LLM updates purpose.md + state_machine.json
        |
        v
same flatc + SDK + semantic gate + commit pipeline
```

If both artifacts changed since the last snapshot, `-sync` refuses to guess and returns an error. This path is for the architect; the sandbox still has no write access to governance.

## 5. Version identity and aliases (Current)

A version is the SHA-256 hash of a canonical three-file bundle:

```text
version_hash = sha256(canonical(purpose.md, protocol.fbs, state_machine.json))
wire_hash    = sha256(canonical(protocol.fbs))
```

The hash is the identity. The date, actor, intent and base version are annotations:

```json
{
  "version": "sha256:...",
  "wire_hash": "sha256:...",
  "created_at": "2026-11-24T19:39:39Z",
  "actor": "manual",
  "intent": "initial litmus contract",
  "base": "sha256:..."
}
```

Three identity layers are kept separate:

| Layer | Purpose | Mutability |
| --- | --- | --- |
| `version_hash` | True identity in manifests and audits | Immutable |
| `alias` | Human name: `v0`, `v1.1`, `stable` | Mutable pointer; `vN` is normally immutable |
| `wire_version` | Compact `uint` in the envelope (Designed) | Allocated at promotion |

Content addressing means:

- identical content resolves to the same version;
- a revert to an old contract resolves to the old hash;
- a new proposal attempt gets a new `draft_id` (Designed), even when its content hash is unchanged;
- dates never change identity.

Layout:

```text
governance/
├── versions/<sha256>/
│   ├── purpose.md
│   ├── protocol.fbs
│   ├── state_machine.json
│   └── meta.json
├── aliases.json
├── actors/<actor>.json
└── compat/<base>__<candidate>.json
```

Current commands:

```bash
go run ./cmd/versionctl snapshot -governance governance -alias v1
go run ./cmd/versionctl alias -governance governance stable v1
go run ./cmd/versionctl pin -governance governance -actor actor_ingest -role producer -version v1
go run ./cmd/versionctl list -governance governance
go run ./cmd/versionctl show -governance governance v1
```

`alias -force` is an explicit administrative correction. Normal evolution allocates a new alias instead of moving a published one.

## 6. Compatibility analysis (Current + Designed)

Compatibility is a relation between two versions and, in the target model, between two actors on a specific edge:

```text
(producer, consumer, message, base_version, candidate_version)
```

The analysis has three layers:

| Layer | Authority | Status |
| --- | --- | --- |
| Wire compatibility | `flatc --conform` | Current |
| Structural diff | `.bfbs` reflection diff | Current |
| Semantic and role impact | Semantic engine (JEV) | Designed |

The current compatibility manifest records:

```json
{
  "base": "sha256:...",
  "candidate": "sha256:...",
  "wire": "compatible",
  "class": "additive",
  "review_required": false,
  "semantic": "not_evaluated",
  "changes": [
    {"kind": "field_added", "object": "litmus.EnrichRequest", "field": "note", "detail": "id 3", "additive": true}
  ],
  "evidence": {
    "flatc_conform": "pass",
    "flatc_version": "flatc version 25.12.19",
    "structural_diff": "pass"
  }
}
```

### Structural classes

| Class | Meaning | Upgrade order | Promotion rule |
| --- | --- | --- | --- |
| `identical` | No structural change. | Any | Promote freely. |
| `additive` | Old peers can ignore the change; wire-compatible. | Any | Promote; old actors remain compatible. |
| `producer_only` | Only the sender must upgrade; old receiver tolerates it. | Producer first | Producer must adopt before the new field is sent. |
| `consumer_only` | Only the receiver must upgrade; old sender still works. | Consumer first | Consumer must adopt before the producer starts sending. |
| `requires_peer_upgrade` | The other side must understand the change. | Coordinated | Block promotion until the required peers have a compatible version or a migration plan. |
| `semantic_change` | Wire-compatible, but behavior or invariants changed. | Review | Requires semantic review; do not auto-promote. |
| `breaking` | Old and new cannot interoperate. | Dual-run / major line | Never promote on the active line; open a major version. |
| `unknown` | The classifier cannot decide. | Blocked | Human review. |

### Why `flatc` is not enough

`flatc --conform` verifies field IDs, types and the wire layout. It does not know the role of each actor, and it may accept changes that are semantically breaking. Example:

```fbs
// base
table EnrichRequest {
  correlation_id:string (required);
  document_id:string (required);
}

// candidate
table EnrichRequest {
  correlation_id:string (required);
  document_id:string (required);
  note:string (required);   // new required field
}
```

`flatc --conform` accepts this, but an old client does not send `note`. The structural diff marks `field_added` as breaking, and the promotion gate must reject it for the active line. This is the concrete reason the reflection diff and the semantic classifier exist.

### Structural rules enforced by the diff

- Field IDs are the wire identity. Never reuse or renumber an ID.
- A field rename is wire-compatible but source-breaking for generated SDK consumers.
- Adding a required field is breaking.
- Removing `required` weakens the contract; it requires semantic review.
- Changing a default, key or optional marker requires review.
- Enum values are append-only; renumbering or reusing a value is breaking.
- Adding a table or enum is generally additive; removing one is breaking.
- Changing `root_type`, the namespace or the file identifier is breaking.

## 7. Draft lifecycle (Designed)

The target model replaces immediate commit with an explicit draft:

```text
proposal
   |
   v
 draft  --(gates + verification)-->  candidate  --(promotion)-->  active  --(superseded)-->  deprecated
   |
   +--(reason)--> abandoned
```

A draft record contains:

```text
draft_id:   01J...        (unique attempt)
base:       sha256:...    (active version it was derived from)
candidate:  sha256:...    (content hash of the proposed artifacts)
status:     draft | candidate | active | abandoned | deprecated
actor:      actor_ingest
intent:     ...
```

Rules:

- `propose_contract_evolution` creates a draft and returns `draft_id` + `candidate_hash`; it does not move the active pointer.
- Drafts are immutable. Editing a draft creates a new revision with a new `draft_id`.
- An actor can be verified against a draft without adopting it.
- An identical re-proposal reuses `candidate_hash` but gets a new `draft_id`.
- Abandoning a draft records a reason, removes ephemeral SDK/test artifacts, and leaves the active contract untouched.
- A draft that is never promoted has no effect on production actors.

### Promotion gate (Designed)

A draft becomes active only when all of the following hold:

1. `flatc --binary --schema` (syntax/types) and `flatc --conform` pass against the chosen base.
2. The structural diff is `additive`, `producer_only`, `consumer_only` or an explicitly approved `semantic_change`.
3. The JEV classifier has produced the semantic/edge impact and the required review is complete.
4. Every actor that must upgrade has a compatible version or an approved migration plan.
5. The owner approves the promotion.
6. For a breaking change, a new major line and a dual-run plan exist.

Promotion writes:

- the new active artifacts;
- the alias (if assigned);
- the wire version mapping (Designed);
- regenerated SDKs and hidden tests;
- a history snapshot and an event record;
- the new actor requirements.

## 8. Adoption and shared contracts (Current pins, Designed matrix)

An actor pin records adoption:

```json
{
  "actor": "actor_ingest",
  "role": "producer",
  "version": "sha256:...",
  "alias": "v1",
  "adopted_at": "2026-11-24T19:39:42Z",
  "status": "active"
}
```

Current behavior:

- `versionctl pin` records the pin; the operator records it after the actor has been verified against the version.
- `versionctl list` shows the number of sides and the adopter names.
- One adopter is a local contract; more than one is a shared contract.

Designed behavior:

- The control plane computes the pairwise matrix for the versions actually in use.
- A mixed-version edge is allowed only if its class permits it.
- Promotion of a shared contract blocks on incompatible edges.
- Breaking changes require a major line, not a matrix exception.
- The owner can approve a migration plan, but the plan is recorded and time-bounded.

Upgrade choreography:

```text
additive:              any order
producer_only:         producer first
consumer_only:         consumer first
requires_peer_upgrade: coordinated
breaking:              dual-run + migration window
```

## 9. Breaking changes and major versions (Designed)

A breaking change never mutates the active line in place.

```text
v1 active
   |
   +--> v2 draft (new namespace / root / file identifier)
          |
          +--> v1 and v2 run side by side
          |       adapters translate at the boundary
          |
          +--> consumers migrate
          |
          +--> v1 deprecated
          |
          +--> v1 retired when no actor depends on it
```

Rules:

- `flatc --conform` failure is a signal, not a dead end: it opens the major-line workflow.
- The new major line has its own version stream and its own hidden suites.
- Dual-run is explicit; there is no silent mixed-version compatibility.
- The old line remains readable until the migration window closes.
- A revert creates a new event pointing at an old hash; it does not rewrite history.

## 10. Field and enum rules

| Element | Rule |
| --- | --- |
| Field ID | Immutable and never reused. Always write explicit `id:`; never rely on declaration order. |
| Field name | Source/API identity. A rename is wire-compatible but source-breaking; add a new field and deprecate the old one. |
| Field type | Immutable. A type change is breaking. |
| Required | Never add a `required` field to an existing table. Required → optional weakens the contract and needs review. |
| Deprecation | Keep the slot. Deprecate instead of deleting in the active line. |
| Enum value | Explicit numeric values; append-only. Never renumber or repurpose. |
| Union | Append-only members. |
| Table / root | Adding tables is safe; removing or renaming tables, changing `root_type` or the namespace is breaking. |
| Invariant ID | `INV-XX` is stable. Never reuse an ID for a different rule. |

## 11. Failure paths

| Failure | Result |
| --- | --- |
| `flatc` syntax/type error | Rejected; exact diagnostics returned; no commit. |
| `flatc --conform` failure | Rejected on the active line; open the major-line workflow if the break is intentional. |
| SDK dry-run failure | Rejected; no commit. |
| Semantic gate reject | Rejected; diagnostics returned; no commit. |
| Semantic gate review | Escalated; no commit; human review required. |
| Semantic gate/provider outage | Proposals fail closed. The gate is mandatory whenever the semantic compiler is enabled; there is no product off switch. |
| Actor verification failure | Minimal counterexample returned; the actor must change, not the contract. |
| Both artifacts edited before `-sync` | Error; the architect must choose one source of truth. |

## 12. Command reference

### Current

```bash
# Actor session
./cmd/sandbox-run/sandbox-run.sh actor_ingest

# Semantic compiler / semantic gate
./bin/boundary-mcp -llm-model ...
./bin/boundary-mcp -sync -governance governance

# Version metadata
go run ./cmd/versionctl snapshot -governance examples/litmus/governance -alias v0
go run ./cmd/versionctl compat -governance examples/litmus/governance -base v0
go run ./cmd/versionctl pin -governance examples/litmus/governance -actor actor_ingest -role producer -version v0
go run ./cmd/versionctl list -governance examples/litmus/governance
go run ./cmd/versionctl show -governance examples/litmus/governance v0
go run ./cmd/versionctl alias -governance examples/litmus/governance stable v0
```

### Make targets

```bash
make litmus
make judge-e2e
make version-snapshot
make version-compat
make version-list
```

`make version-*` targets default to `examples/litmus/governance`; override with `VERSION_GOVERNANCE=...`.

### Designed (not wired yet)

```text
versionctl draft ...      create/revise a draft
versionctl promote ...    run the promotion gate and move the active pointer
versionctl abandon ...    mark a draft abandoned with a reason
```

These commands are intentionally not exposed until the compiler supports draft-first proposals.

## 13. Implemented vs designed

| Capability | Status |
| --- | --- |
| Actor context and read-only SDK | Current |
| Hidden-suite verification | Current |
| `propose_contract_evolution` auto-commit | Current |
| Manual `-sync` reconciliation | Current |
| Semantic gate (JEV) enforced when the compiler is enabled | Current |
| History snapshots and diffs | Current |
| Content-addressed version snapshots | Current |
| Aliases and actor adoption pins | Current |
| `flatc --conform` + reflection compatibility manifest | Current |
| Draft → candidate → active lifecycle | Designed |
| Promote / abandon operations | Designed |
| JEV role-specific compatibility (`producer_only`, `consumer_only`, `requires_peer_upgrade`) | Designed |
| Pairwise adoption matrix and promotion policy | Designed |
| `protocol_version:uint` in the envelope | Designed |
| Breaking-change major line and dual-run | Designed |
| Append-only event log for alias/promotion/adoption events | Designed |
| Sandboxed verifier subprocesses | Hardening item |
