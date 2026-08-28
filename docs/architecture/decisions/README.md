# Architecture decisions

Architecture decision records (ADRs) document choices that shape Forge across
features or interfaces. They preserve the context, invariants, consequences,
and boundaries of a decision so later work does not have to reconstruct them
from pull requests or conversations.

An ADR has one of these statuses:

- **Proposed**: under review and not yet an architectural constraint.
- **Accepted**: the current decision and an authority for implementation.
- **Implemented**: an accepted decision whose required delivery is complete.
- **Superseded**: retained as history but replaced by a later ADR.
- **Rejected**: considered and deliberately not adopted.

Accepted and implemented ADRs may be changed only by a later ADR that records
why the decision changed. Implementation details may evolve without a new ADR
when they continue to preserve the accepted decision and its invariants.

An ADR that depends on another proposed ADR cannot become Accepted until that
dependency is accepted or implemented.

## Decision log

| ADR | Status | Decision |
| --- | --- | --- |
| [0001](0001-agent-native-forge-domain.md) | Proposed | Domain authority |
| [0002](0002-native-semantic-mcp.md) | Implemented | Native semantic MCP |
| [0003](0003-authoritative-work-planning.md) | Proposed | Authoritative work planning |
| [0004](0004-safe-mcp-work-planning.md) | Proposed | Safe MCP work planning |
