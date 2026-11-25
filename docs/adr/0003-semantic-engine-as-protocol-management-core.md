# ADR-0003: The Semantic Engine Is the Protocol-Management Core

- **Status:** Accepted
- **Date:** 2026-11-25
- **Deciders:** boundary-mcp maintainers
- **Supersedes:** the "advisory / Phase 6 experiment" framing of [ADR-0001](0001-advisory-semantic-judge.md). The decision to use JEV as the semantic gate stands; only its position in the architecture changes.

## Context

Two different questions govern a protocol:

1. **Is this the right protocol?** Does the change serve the declared intent, preserve invariants, stay in scope, and keep `purpose.md` and `protocol.fbs` describing the same behavior? This is a question about meaning, and it has no deterministic answer.
2. **Is this protocol well-formed and compatible?** Does it compile, does it keep wire compatibility, does an implementation honor it? These have deterministic answers.

`flatc` answers the second group with authority. It cannot answer the first. Calling `flatc` "the contract oracle" conflates conformance with meaning and leaves the first question to nobody.

ADR-0001 introduced JEV to answer the first question, but wrote it as an "advisory Phase 6 experiment" that "must never replace `flatc` or the harness as the contract oracle". The implementation then ran it in `enforce` mode from the start, mandatory and fail-closed. Documentation kept the experimental framing while the code treated the gate as required, so the architecture read as a deterministic product with an optional semantic add-on.

That framing has two costs:

- **It misdescribes the product.** No supported mode runs the semantic compiler without the gate. The component the documentation calls optional is the one that decides whether a change is legitimate.
- **It invites workarounds.** An agent optimizing for a passing local state will route around a gate described as optional or advisory. A soft semantic layer on the only write path is a gate that gets argued with.

The deterministic layers must keep their authority at the same time. A semantic judgment must never approve a schema that does not compile, deduce wire compatibility, or declare an actor correct.

## Decision

### 1. Four layers with explicit domains

| Layer | Question | Mechanism | Character |
| --- | --- | --- | --- |
| Intent | Is this the right protocol? | Semantic engine (JEV judgments + policy) | Subjective, probabilistic |
| Conformance | Is it expressible and compatible? | `flatc --binary --schema`, `--conform`, reflection diff | Deterministic |
| Behavior | Does the implementation honor it? | `internal/harness` hidden suites | Deterministic |
| Identity | Which protocol is in force, for whom? | `internal/versioning` content hashes, aliases, pins | Deterministic |

Each layer is authoritative **inside its own domain** and cannot overrule another's. The semantic engine cannot approve an uncompilable or incompatible schema. Neither `flatc` nor the harness can declare a change legitimate. This replaces "the judge is never the contract oracle" with a domain split: `flatc` is the conformance oracle and the semantic engine is the intent oracle.

### 2. The engine drives; the deterministic layers execute

Protocol management is the act of deciding what the contract should mean and whether a proposed change may take effect. That decision belongs to the semantic engine. `flatc`, the harness and the version store are the executors and the ledger: they establish that the decision is expressible, honored and attributable.

The engine therefore leads the architecture rather than appearing as an appendix to the deterministic core.

### 3. Enforcement is part of the decision, not a configuration

The gate is mandatory whenever the semantic compiler is enabled, and it fails closed: without a resolved key, the semantic compiler does not start. The knobs that disable or soften it are test-only and stay outside the product path.

A probabilistic gate is only hard if its verdict is stable: identical content must resolve to the same judgment, and an escalation must not be re-rolled by resubmitting. That binds the engine to the identity layer (§1) instead of leaving versioning as an unrelated prototype.

### 4. "Advisory" is retired as a description of this component

The component is the **semantic gate** (or **semantic engine**). "Advisory" described the original experiment and now contradicts the enforced behavior. ADR-0001 keeps its title as the historical record.

### 5. Construction order is not architecture

The Phase numbering in the roadmap records the order in which the system was built. It is not a hierarchy. The semantic engine is part of the core from the moment the semantic compiler is enabled, regardless of which phase introduced it.

## Consequences

**Positive**

- The documentation matches the enforced behavior; agents cannot cite the docs to argue that the semantic gate is optional.
- The division of authority is explicit: no decisive layer is described as subordinate.
- The identity layer gains a stated purpose beyond bookkeeping: it makes a probabilistic verdict stable and auditable.

**Negative**

- The semantic engine is now a declared product dependency. Its failure modes — provider outage, model deprecation, latency — are product failure modes, not experiment issues.
- The claim depends on the follow-ups below; without them the gate is less hard than this ADR says.

## Follow-ups

These requirements are implied by this decision and are not implemented by it:

1. Persist every verdict, including rejected and escalated proposals, with the bundle hash. A blocked proposal currently leaves no record in `governance/history/`.
2. Bind the verdict to the content hash so that resubmitting identical content cannot resample a probabilistic judgment.
3. Route the review band to a human queue; an `escalated` result currently has no destination.
4. Require `suite.build` and sandbox the verifier's build/run subprocesses, so an actor cannot influence its own verification.
5. Fail the write path, not startup, when the provider credential is missing.

## References

- [ADR-0001: Advisory Semantic Judge](0001-advisory-semantic-judge.md)
- [ADR-0002: Content-Addressed Protocol Versions](0002-content-addressed-protocol-versions.md)
- [Architecture specification, §1](../ARCHITECTURE.md#1-overview-and-architectural-thesis)
- [README: semantic engine](../../README.md#semantic-engine-protocol-management)
