# ADR 0001: Make the Forge domain authoritative for human-agent collaboration

- Status: Proposed
- Date: 2026-08-26
- Decision owner: Forge maintainer

## Context

Forge is an independent software forge built from the Gitea 1.27.2 substrate.
Its inherited repositories, issues, pull requests, checks, authentication,
permissions, and persistence solve much of the undifferentiated work of hosting
software projects. Forge can therefore focus on collaboration between humans
and software agents without rebuilding the Git hosting substrate first.

Existing forge interfaces are principally designed around human web workflows
or low-level REST resources. Agent integrations reconstruct project intent,
permissions, policy, and related state from those interfaces. Human and agent
work can consequently produce parallel interpretations or state that exists
only in one interface or conversation.

Forge needs one authoritative domain that both humans and agents can observe and
act upon. A native Model Context Protocol (MCP) server is an important agent
interface, but no transport should define the domain model it exposes.

## Decision

The Forge domain is the narrow waist between the inherited substrate and every
human, agent, or integration interface:

```text
Human web UI       Semantic MCP       REST / integrations
       \                 |                 /
        +---------- Forge domain ----------+
                         |
                         v
                  Git hosting substrate
```

Forge will define application operations in responsibility-oriented service
boundaries. Interfaces may present different affordances, but they must derive
authorization, state transitions, and durable results from those shared
operations.

The Forge domain is an architectural boundary, not a mandate for one package,
one service, or one universal object model. Focused modules should continue to
own repository, issue, pull request, check, policy, and identity
responsibilities. The shared domain must not become a `forge/domain` grab bag
that merely moves existing coupling behind a new name.

Transport handlers may authenticate requests, validate transport-specific
syntax, select representations, and map errors. They must not become independent
authorities for domain policy or state transitions. Existing interfaces can
migrate toward shared operations incrementally; this decision does not require
rewriting the inherited application before the first agent-native capability
ships.

## Domain invariants

### One authoritative project state

Important collaboration state belongs to Forge, not to an agent transcript,
browser session, protocol session, or local adapter. Sessions may cache or
summarize state, but losing a session must not lose the authoritative record of
a decision, review, policy, claim, or evidence item.

### Human-agent symmetry

Anything material an agent creates must be legible and manageable through human
affordances. Anything material a human creates must be discoverable and, when
authorized, actionable through agent affordances.

This symmetry applies to each capability Forge introduces or migrates. It does
not require the first agent projection to reproduce every inherited human
workflow before a useful vertical slice can ship.

The projections do not need identical shapes. They must represent the same
underlying facts and preserve the same invariants. Forge has failed this test if
a human must read an agent transcript to understand agent work, or if an agent
must scrape rendered HTML to discover a policy created in the web UI.

This invariant includes material provenance. When Forge can verify that an
artifact originated through an agent-facing interface, the human interface must
make that origin discoverable without claiming an unverified agent identity.

### Principal and actor are distinct

Forge will distinguish:

- **Principal**: the Forge identity whose authority permits an operation.
- **Actor**: the human or software agent that performed the operation.
- **Credential**: the authentication artifact binding the request to authority.
- **Operation**: the domain action attempted under that authority.
- **Artifact**: the durable Forge object created or changed by the operation.

A direct human action may have the same principal and actor. A delegated agent
action may identify a human principal and a different agent actor. Authorization
is evaluated from the principal, credential, repository policy, and requested
operation; naming an actor never grants authority.

Existing personal access tokens can authoritatively identify their owning
principal, but they do not prove which client or agent used them. An interface
may accept those tokens, but it must represent a distinct actor as unknown or
unverified. Legacy records may still identify the authenticated user, but new
provenance must not claim that the human personally performed the operation.
Forge must never accept a self-reported actor on an individual operation as
trusted provenance.

Verified agent attribution requires a Forge-issued delegated credential that
binds an agent identity, principal, scopes, lifetime, and relevant resource
limits. The model must support the distinction before delegated credentials are
implemented.

This invariant is forward-binding. It does not require a delegated credential
or distinct actor subsystem for the first read-only agent projection.

### Evidence is revision-bound

Verification evidence is meaningful only in relation to the exact repository
state it examined. Durable evidence must identify the commit or immutable
artifact it supports, who or what produced it, how it was produced, and whether
Forge observed the result or merely received a claim.

Evidence attached to an older revision may remain useful history, but it must
not silently satisfy a policy requiring verification of a newer revision.

This invariant is forward-binding while Forge designs a general evidence
object. Existing check and commit-status results are its initial concrete
instance: their revision association must be preserved by every projection.

### Policy is server-enforced and inspectable

Forge remains the authority for repository permissions, protected branches,
required checks, and future agent-specific policy. Every projection must enforce
the same applicable policy at the domain boundary.

For each capability Forge introduces or migrates, agents must also be able to
inspect the policies relevant to an operation that capability exposes before
attempting it. Discoverability does not weaken enforcement and must not reveal
resources the principal cannot access.

### Agent output is evidence, not authority

Agents may propose, implement, test, and review changes. Those actions can
produce evidence and durable artifacts, but they do not replace the maintainer's
authority described in [Project governance](../../community-governance.md).

## Consequences

### Benefits

- Human and agent interfaces converge on one authority for state, permission,
  policy, and provenance.
- New interfaces reuse domain decisions instead of reconstructing them from
  transport-shaped resources.
- Agent work becomes durable and legible outside the conversation that produced
  it.
- Principal, actor, credential, revision, and evidence can support precise
  provenance instead of bot-name conventions.
- Product behavior can evolve without making one protocol the internal
  architecture of Forge.

### Costs and risks

- Shared application operations require deliberate boundaries in inherited code
  that was often organized around web or REST flows.
- Human-agent symmetry adds presentation and management work to capabilities
  that might otherwise exist only as backend records.
- Supporting both personal tokens and future delegated agent credentials creates
  an interim period where the principal is verified but the distinct software
  actor is not.
- A poorly bounded interpretation of "Forge domain" could create a new monolith
  instead of clarifying responsibility.

These costs are accepted because interface-owned semantics would create
parallel sources of truth and make later human-agent features harder to
integrate safely.

## Rejected alternatives

### Let each interface own its workflows

Rejected because web, REST, and agent interfaces would answer the same domain
questions differently and produce state that other collaborators cannot fully
observe or manage.

### Implement agent-only objects and workflows

Rejected because humans could not reliably discover or manage them and other
interfaces would observe a different project reality.

### Make one universal domain package

Rejected because Forge contains several enduring responsibilities with different
invariants. One package would hide coupling rather than establish clear
authorities.

### Refactor all inherited interfaces before adding agent capabilities

Rejected because it delays validation and creates a speculative,
repository-wide rewrite. Shared operations will be introduced around useful
vertical slices, then adopted elsewhere when that removes duplicated authority.

## Acceptance criteria

An implementation conforms to this decision when:

- at least one agent-facing vertical slice and its human-facing counterpart use
  the same application operation as the domain authorization authority and, for
  mutations, the state-transition authority; transport middleware may retain an
  outer authorization gate but may not substitute for the shared decision;
- automated dependency rules prevent that agent transport from invoking web or
  REST transport handlers;
- when agent-facing mutations are introduced, their material artifacts are
  visible and manageable through a human interface;
- policy applicable to an inspected operation, including its evaluated
  blockers, is discoverable through an authorized agent interface without HTML
  scraping;
- when agent-facing mutations or delegated credentials are introduced,
  provenance represents principal, actor, credential, and interface origin
  without promoting unverified client metadata into trusted identity;
- revision-associated checks retain the same commit identity through every
  projection; and
- permission-boundary tests demonstrate that every projection introduced or
  migrated by the slice enforces the same domain decision.

## Related decisions

[ADR 0002](0002-native-semantic-mcp.md) defines the first agent-native protocol
projection and its initial semantic vocabulary.

## Deferred decisions

Separate decisions will define:

- delegated agent enrollment, credential issuance, revocation, and display;
- durable evidence, work claims, proposals, and handoff objects; and
- retention and privacy policy for agent provenance and audit events.
