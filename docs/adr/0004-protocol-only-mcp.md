# ADR-0004: boundary-mcp Is a Protocol Manager, Not an Agent Runtime or CI

- **Status:** Accepted
- **Date:** 2026-11-25
- **Deciders:** boundary-mcp maintainers
- **Supersedes:** the actor-runtime and actor-behavior-verification scope described in [ARCHITECTURE.md](../ARCHITECTURE.md) §6–§7 and [WORKFLOWS.md](../WORKFLOWS.md) §2–§3. [ADR-0001](0001-advisory-semantic-judge.md) and [ADR-0002](0002-content-addressed-protocol-versions.md) remain in force.
- **Implementation plan:** [docs/PLAN.md](../PLAN.md)

## Context

The repository accumulated three responsibilities:

1. **Contract management** — `purpose.md` ↔ `protocol.fbs` ↔ `state_machine.json`, `flatc` gates, the semantic engine, versions and compatibility.
2. **Agent runtime** — `cmd/sandbox-run` launched a pinned `opencode` inside `bwrap`.
3. **Actor behavior verification** — `verify_actor_behavior` built and ran the actor binary against hidden suites.

Responsibilities 2 and 3 were introduced to make the Phase 5 disposability experiment reproducible. They then became the documented entry point and the documented isolation model. That promotion produced consequences the project did not intend:

- the user's coder became a dependency of the contract manager, because the manager launched it;
- `verify_actor_behavior` executed code the server did not write, on the server's host, and accepted an agent-supplied `entrypoint_cmd` (`internal/harness/verifier.go:75-103`);
- the MCP assumed filesystem co-location with actor source (`actors/<name>`), although actor code never travels through the protocol;
- "capability-based physical isolation" became a claim about a property only the agent's own runtime can provide;
- hidden suites existed to prevent overfitting to behavioral tests — tests the system only needed while it ran them.

MCP is the interface, not the system's scope. A manager that speaks MCP may run `flatc`, the semantic engine and the version store; it does not follow that it must run the coder or the actor's binary.

## Decision

### 1. Scope

boundary-mcp owns **contract management** and **protocol testing**. It does not own the coder, the actor's source, actor execution, or merge authority.

### 2. Actor code never enters the system

The server never receives, reads or executes actor code, and exposes no tool that takes a build or run command. Any future capability requiring an actor directory belongs to an example or to the user's CI.

### 3. The coder is an external MCP client

The user runs their own coder in the actor's service directory. Isolation is that directory layout plus the coder's own `external_directory: deny` rule; the server publishes the recommended rule and does not attempt to enforce filesystem visibility.

### 4. Protocol tests are normative, public and content-addressed

The server publishes a declarative test specification as an MCP resource, addressed by the protocol content hash. The spec carries closed semantics and a deterministic case set. It contains no execution keys. It is public: protocol tests are shared by construction, and hiding them only protected behavioral tests, which leave the system.

### 5. Gate authority is CI

The server provides the normative spec and its version. The user's CI runs it against the actor directory and decides whether a merge proceeds. The server cannot claim enforcement it does not perform.

### 6. `sandbox-run` is a fixture

The `bwrap` launcher moves under `examples/` and keeps its role in reproducing the disposability experiment. It is not the product path.

## Consequences

**Positive**

- The dependency graph matches the product: `coder → MCP server`, not `server → launches coder`.
- The server no longer executes untrusted code; the largest hardening surface disappears with the execution path.
- No code-delivery contract is needed, because no code is delivered.
- The interface becomes two small families: management and protocol testing.
- The disposability experiment remains reproducible without being sold as architecture.

**Negative**

- The server cannot verify an actor's behavior; teams that want behavioral gates must run them in CI.
- The server cannot guarantee that an agent did not read a sibling directory; that becomes a workflow convention plus a coder-side rule.
- Removing `verify_actor_behavior` is a breaking MCP change.
- A substantial part of `internal/harness` moves out of the product, and the docs that describe the old model must be rewritten.

## Follow-ups

See [docs/PLAN.md](../PLAN.md) for staging and acceptance criteria. In order: publish the spec and resources, delete the execution path, split the harness, move the launcher, rewrite the docs, scope the token, document CI as the gate.

## References

- [ADR-0001: Advisory Semantic Judge](0001-advisory-semantic-judge.md)
- [ADR-0002: Content-Addressed Protocol Versions](0002-content-addressed-protocol-versions.md)
- [ADR-0003: The Semantic Engine Is the Protocol-Management Core](0003-semantic-engine-as-protocol-management-core.md)
- [Plan: Protocol-Only boundary-mcp](../PLAN.md)
