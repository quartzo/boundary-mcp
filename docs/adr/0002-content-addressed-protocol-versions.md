# ADR-0002: Content-Addressed Protocol Versions, Compatibility Classes, Adoption and Aliases

**Status:** Accepted

**Context:** boundary-mcp already has a single governed evolution path: agents propose through `propose_contract_evolution`, `flatc` checks syntax and wire compatibility, the JEV judge reviews semantics, and accepted changes are committed with a history snapshot. That is evolution management, but it is a single active line with no explicit version identity, no draft lifecycle, no abandonment record, and no way to reason about which actors have adopted a change.

The project now needs to make protocol evolution observable and negotiable:

- every contract state needs an immutable identity;
- compatibility must be a relation between two versions, not a label on one version;
- a change can be additive, producer-only, consumer-only, peer-upgrade-required or breaking;
- adoption must be tracked per actor so a shared contract can be administered;
- humans need aliases and numbering, while machines need stable hashes and a compact wire version;
- drafts must be promotable or abandonable without changing the active contract.

## Decision

### 1. Identity is a content hash

A version is the SHA-256 hash of a canonical three-file bundle:

```text
version_hash = sha256(canonical(purpose.md, protocol.fbs, state_machine.json))
wire_hash    = sha256(canonical(protocol.fbs))
```

The version hash is the immutable identity. The creation date, author, intent, base version and judge verdict are annotations. Dates are never hashed into the identity: the same contract proposed twice must resolve to the same version, and reverting to an old contract must resolve to the old hash.

Each proposal attempt has a separate `draft_id` (for example a ULID). This distinguishes “what the contract is” (`candidate_hash`) from “this attempt to introduce it” (`draft_id`). An abandoned draft and a later identical proposal share the content hash but have different draft IDs.

### 2. Compatibility is a relation

Compatibility is always evaluated as:

```text
(producer, consumer, message, base_version, candidate_version)
```

`flatc --conform` is authoritative for wire compatibility. A reflection diff records structural changes. The advisory judge classifies semantic and role-specific impact. The resulting classes are:

| Class | Meaning | Upgrade order |
| --- | --- | --- |
| `additive` | Old peers can ignore the change; wire-compatible | Any order |
| `producer_only` | Only the sender must upgrade; old receiver tolerates it | Producer first |
| `consumer_only` | Only the receiver must upgrade; old sender still works | Consumer first |
| `requires_peer_upgrade` | The other side must understand the change | Coordinated |
| `breaking` | Old and new cannot interoperate | Dual-run or new major line |
| `semantic_change` | Wire-compatible, but behavior or invariants changed | Review required |
| `unknown` | Needs human review | Blocked |

The LLM/JEV is never the authority for wire compatibility. It fills the semantic and role-specific parts; `flatc` and the structural diff remain deterministic.

A required field added to an existing table is a concrete example: `flatc --conform` may accept it, but old peers do not send it. The structural diff must mark it breaking.

### 3. Adoption is tracked per actor

Each actor pins a version and a role:

```json
{
  "actor": "actor_ingest",
  "role": "producer",
  "version": "sha256:...",
  "alias": "v1",
  "status": "active"
}
```

A version adopted by one actor is a local contract. A version adopted by more than one actor is a shared contract. Promotion of a shared contract requires compatibility checks across all affected edges, not just an adoption count.

The control plane eventually computes a pairwise matrix for the versions in use. A mixed-version edge is allowed only when its class says it is safe. Breaking changes require a new major line and a migration window; they never mutate the active line in place.

### 4. Aliases are human labels, not identity

Three layers are kept separate:

| Layer | Purpose | Mutability |
| --- | --- | --- |
| `version_hash` | True identity; actor manifests and audits | Immutable |
| `alias` | Human name (`v1`, `v1.1`, `stable`) | Mutable pointer, audited |
| `wire_version` | Compact `uint`/enum in the envelope | Allocated at promotion |

Published `vN` aliases are immutable; `stable` and `latest` may move. The CLI exposes `-force` only as an explicit administrative correction; normal evolution allocates a new alias. Aliases are never used as identity in actor manifests or wire data. A 64-character hash is not placed in every frame; the envelope carries a compact version that the governance registry maps to a hash.

### 5. Drafts are explicit

A proposal creates a draft; it does not immediately move the active pointer:

```text
draft_id:  01J...
base:      sha256:...
candidate: sha256:...
status:    draft | candidate | active | abandoned | deprecated
```

Actors can be verified against a draft without adopting it. Promotion moves an alias or active pointer and records the adoption requirements. Abandonment records a reason and leaves the active contract untouched. Drafts are immutable; editing a draft creates a new revision.

The current auto-commit compiler is the first gate, not the final lifecycle. Draft and promotion tools are a follow-up.

### 6. Wire version

The envelope eventually carries `protocol_version:uint` (or an equivalent compact field). The governance registry maps that number to a version hash. Runtime compatibility checks use the static compatibility matrix; no LLM runs on the data plane.

## Consequences

**Positive**

- Every contract state is content-addressed, reproducible and auditable.
- Compatibility is explicit, per edge, and can be checked before promotion.
- Actors can be tested against drafts without changing the active contract.
- Aliases give humans stable names without weakening machine identity.
- Abandoned drafts and rejected proposals can be recorded without affecting production.

**Negative**

- The registry, alias and actor files add governance state that must be maintained.
- Compatibility is only as good as the role model in `purpose.md` and `state_machine.json`; missing roles become review cases.
- A version hash is not a wire version; the mapping must be deployed with the actors.
- Draft/promotion adds an administrative step to the current auto-commit flow.

## Prototype Implemented

This decision is implemented incrementally. The first slice is read-only with respect to the active contract:

- `internal/versioning`: canonical bundle hashing, immutable version directories, aliases, actor pins, `flatc --conform` + reflection-diff compatibility analysis.
- `cmd/versionctl`: `snapshot`, `compat`, `list`, `show`, `alias`, `pin`.
- `examples/litmus/governance` contains the `v0` snapshot, `v0` alias and actor pins for `actor_ingest` and `actor_enrich`.

The semantic/role-specific classifier, draft/promote/abandon MCP tools, pairwise adoption matrix, wire-version field and promotion policy are follow-ups. Alias moves and actor pins currently record current state; an append-only event log is a follow-up. The compiler’s auto-commit behavior is unchanged in this slice.

## Follow-ups

1. Add a JEV compatibility classifier that emits `producer_only`, `consumer_only`, `requires_peer_upgrade` and per-edge impact, with evidence and confidence.
2. Add `draft`, `promote` and `abandon` operations to the compiler/MCP, with `propose` returning a draft ID instead of committing.
3. Add `protocol_version:uint` to the envelope and map it to the version hash.
4. Compute and enforce the pairwise compatibility matrix from actor pins.
5. Add a promotion policy (owner approval plus required-actor compatibility) and migration windows for breaking changes.
6. Add an append-only governance event log for alias moves, adoption changes, promotions and abandonments.

## References

- [Architecture specification, §5](../ARCHITECTURE.md#5-boundary-mcp-tool-specifications)
- [ADR-0001: Advisory Semantic Judge](0001-advisory-semantic-judge.md)
- [README: protocol versions](../../README.md#protocol-versions-and-compatibility)
