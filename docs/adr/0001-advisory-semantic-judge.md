# ADR-0001: Advisory Semantic Judge for Contract Evolution

- **Status:** Proposed (Phase 6 experiment)
- **Date:** 2026-11-24
- **Deciders:** boundary-mcp maintainers
- **Supersedes:** none

## Context

boundary-mcp keeps contract correctness deterministic: `flatc --binary --schema` validates syntax, `flatc --conform` rejects backward-incompatible changes, and `internal/harness` decides whether an actor obeys the contract. Those gates are the contract oracle and must remain so.

The semantic compiler (`internal/compiler`) uses a reasoning LLM to propose complete `purpose.md`, `protocol.fbs` and `state_machine.json` contents. That model is expensive, slower, and non-deterministic. It can also produce changes that satisfy `flatc` while drifting from the verbal purpose: removing an invariant, changing behavior outside the requesting actor's scope, or failing to address the stated intent.

The litmus test also needs qualitative evaluation of the reconstructing agent: boundary adherence, scope discipline, overfitting and completeness. Running a reasoning model for every small judgment is unnecessary.

System One models such as JEV return fast, calibrated typed judgments (`Choice`, `Score`, `Noul`) instead of generated text. They are well suited to narrow verification and routing, but they do not write code, generate schemas, or explain reasoning.

## Decision

Add an **advisory semantic judge** as an optional Phase 6 layer:

1. Deterministic gates run first and stay authoritative.
2. The judge consumes the proposal (intent, purpose, protocol, state machine, diff) and returns narrow `Noul` probabilities for explicit semantic rules.
3. Code combines the probabilities with the deterministic results.
4. Low-confidence or high-risk cases escalate to a reasoning model or a human; clear cases proceed without a second large LLM call.

The judge is never the contract oracle. A `flatc` failure is always a rejection, regardless of the judge. A judge signal can add an escalation or a diagnostic, but it cannot approve a contract that deterministic gates reject.

### Transport

JEV is reached through **OpenRouter's typed System One endpoint**, so the project reuses its existing OpenRouter account and API key:

- `POST https://openrouter.ai/api/v1/systemone`
- `Authorization: Bearer $OPENROUTER_API_KEY`
- model pinned to `typesafe/jev-1.14` (or the `jev-latest` alias during exploration)

OpenRouter preserves the TypeSafe request/response shape (`state`, `questions`, `answers`, `usage`) and additionally returns `usage.cost`, which the benchmark records. The client caches verdicts by the canonical state hash.

For local development, when `OPENROUTER_API_KEY` is unset, the client reads the OpenRouter key from opencode's auth store (`$XDG_DATA_HOME/opencode/auth.json`, override with `OPENCODE_AUTH_FILE`). Production deployments should set the environment variable.

### Rollout

The project has no production traffic. The Phase 6 experiment runs in **enforce** mode from the start; a record-only mode exists only as a debugging aid for the benchmark. The deterministic gates and the `judge=off` switch are the safety net, so there is no reason to stage a shadow rollout. The CLI supports `judge=auto`, which enables enforce when the semantic compiler and a key are configured; `sandbox-run` uses auto by default. `make judge-e2e` exercises the live compiler-to-judge path, while unit tests stay offline with a fake judge.

### Candidate rules for proposals

- `removed_or_renamed_field`: the proposal removes or renames an existing field instead of deprecating it.
- `dropped_invariant`: an invariant in `purpose.md` disappears or loses its hard-contract language.
- `out_of_scope`: the change modifies actors, messages or invariants unrelated to the requesting actor.
- `intent_mismatch`: the `protocol.fbs`/`purpose.md` diff does not address the declared intent.
- `purpose_protocol_drift`: `purpose.md` and `protocol.fbs` describe different behavior.
- `unsafe_optionality`: a field becomes optional or loses validation in a way the purpose does not justify.
- `invented_scope`: a new actor, message or invariant appears that is unrelated to the intent and existing purpose.

Each rule is one question with explicit true/false criteria. Rules are decomposed so a fired signal names the problem.

### Cascade rules (initial)

- `flatc` rejects → reject; forward the `flatc` diagnostic unchanged.
- `flatc` accepts and every judge rule has `P(rule broken) < 0.40` → accept.
- `flatc` accepts and any judge rule has `P(rule broken) ≥ 0.70` → reject or escalate to a reasoning model or human review.
- Otherwise → review queue.

The 0.40 review threshold comes from the synthetic calibration below; 0.70 is the initial rejection threshold. Both must be revalidated on real proposals.

## Non-goals

- Generating `purpose.md`, `protocol.fbs`, `state_machine.json`, actor code or test code.
- Replacing `flatc`, the harness, or the deterministic PASS/FAIL verdict.
- Multi-step planning, long-context repository analysis or free-form explanations.
- Using a typed answer as proof of correctness.

## Experiment plan

**Dataset.** At least 100 labeled proposals spanning compatible additions, breaking changes, deprecated fields, invariant removal, out-of-scope edits, intent mismatch and no-op changes. Labels include the `flatc` outcome and the desired semantic verdict. Cases are JSON files; `cmd/judge-eval` loads a directory of them and prints decisions, confusion counts, false allows, false escalations, p95 latency and `usage.cost`.

**Synthetic calibration (2026-11-24).** 100 template-generated cases, ten families × ten variants: three compatible families, six semantic-violation families and one ambiguous family. Through `cmd/judge-eval` and `typesafe/jev-1.14`:

| review threshold | correct | false allows | false escalations (allow) | review → reject |
| --- | ---: | ---: | ---: | ---: |
| 0.30 | 82/100 | 0 | 8 | 10 |
| 0.40 | 90/100 | 0 | 0–1 | 9–10 |

Compatible probabilities maxed at 0.39–0.42 across two runs; semantic violations started at 0.89. p95 was 356–558 ms and the whole run cost $0.00546 (about $0.00005 per case). JEV probabilities vary between runs, so cache and repeat before drawing conclusions. The 0.40 default comes from this separation. The set is template-generated, so these numbers are a seed calibration, not proof of generalization; revalidate on real proposals before accepting this ADR.

**Metrics.**

- per-rule precision, recall and macro F1;
- calibration (reliability curve and Brier score) on the project's data;
- false-accept rate for changes that should escalate;
- escalation rate on genuinely compatible proposals;
- p50/p95 latency and cost per 1,000 proposals;
- reduction in reasoning-model calls and review time.

**Success criteria (proposal, to confirm before adoption).**

- No increase in false accepts versus the deterministic baseline on the labeled set.
- Recall ≥ 0.90 on semantic-drift categories, with false-escalation ≤ 10% on compatible proposals.
- p95 judge latency ≤ 500 ms and judge cost < 5% of the reasoning-model call it replaces.
- Thresholds and labels are reproducible and stored with the evaluation.

**Rollback.** The judge is behind configuration (`judge=off`) and can be disabled without changing the compiler or harness. If the experiment fails, this ADR is marked `Rejected` and the system keeps the deterministic-only flow.

## Consequences

**Positive.**

- Cheaper and faster semantic checks than a second reasoning-model call.
- Catches drift classes `flatc` cannot see.
- Structured, loggable judgments that can be re-thresholded without rerunning inference.
- Reusable evaluation signals for the litmus and CI.

**Negative.**

- New optional external dependency and credential to manage.
- Thresholds require domain calibration; probabilities are not certainty.
- Governance text leaves the host for a third-party service (OpenRouter / TypeSafe). The LLM provider already receives it during proposal generation; this adds another processor.
- A provider outage or rate limit can block proposals in enforce mode. The judge client fails closed; `judge=off` is the escape hatch.
- Risk that operators treat a typed answer as a hard gate.

## Alternatives considered

- **Deterministic-only (status quo).** Cheapest and safest, but blind to semantic drift.
- **LLM-as-judge.** Stronger reasoning, but slow, expensive, non-deterministic and hard to calibrate.
- **Deterministic semantic lint.** Useful and preferred where rules are expressible in code (for example field IDs and required fields); cannot handle natural-language intent.
- **Human review for every proposal.** Correct but does not scale.

## References

- [OpenRouter System One / TypeSafe SDK guide](https://openrouter.ai/docs/guides/community/typesafe-sdk)
- [OpenRouter TypeSafe models](https://openrouter.ai/typesafe)
- [OpenRouter Go SystemOne SDK](https://openrouter.ai/docs/client-sdks/go/sdks/systemone/README)
- [System One](https://docs.typesafe.ai/concepts/system-one.md)
- [Confidence](https://docs.typesafe.ai/confidence.md)
- [SDE cascade](https://docs.typesafe.ai/cookbooks/sde_cascade.md)
- [Use cases: agents and verification](https://docs.typesafe.ai/concepts/use-case-map.md)
- [boundary-mcp architecture, §5.2](../ARCHITECTURE.md#52-propose_contract_evolution)
- [boundary-mcp architecture, §6](../ARCHITECTURE.md#6-deterministic-test-induction-and-execution-engine)

## Notes

"JEV" and "System One" refer to TypeSafe's model family. Product details and thresholds in this ADR are proposals, not commitments; validate them on the project's data before adopting.
