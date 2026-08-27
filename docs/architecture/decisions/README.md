# Architecture decisions

Architecture decision records (ADRs) document choices that shape Forge across
features or interfaces. They preserve the context, invariants, consequences,
and boundaries of a decision so later work does not have to reconstruct them
from pull requests or conversations.

An ADR has one of these statuses:

- **Proposed**: under review and not yet an architectural constraint.
- **Accepted**: the current decision and an authority for implementation.
- **Superseded**: retained as history but replaced by a later ADR.
- **Rejected**: considered and deliberately not adopted.

Accepted ADRs may be changed only by a later ADR that records why the decision
changed. Implementation details may evolve without a new ADR when they continue
to preserve the accepted decision and its invariants.

## Decision log

| ADR | Status | Decision |
| --- | --- | --- |
| [0001](0001-agent-native-forge-domain.md) | Proposed | Domain authority |
| [0002](0002-native-semantic-mcp.md) | Proposed | Native semantic MCP |
