# ADR 0004 client-onboarding delegated execution

- Status: Active execution projection
- Date: 2026-08-29
- Authority commit: `8df45e530fee1bcdacf7ce729d8c2a90b9da6894`
- Decision: [ADR 0004](../decisions/0004-safe-mcp-work-planning.md)
- Work packages:
  [ADR 0004 implementation plan](0004-mcp-work-planning-implementation.md)
- Predecessor:
  [ADR 0004 swarm orchestration projection](0004-mcp-work-planning-orchestration.md)

This projection supersedes the predecessor's Agent K-N seat assignments and
WP11-WP14 execution mechanics. The predecessor's semantic boundaries,
acceptance gates, and historical WP0-WP10 record remain in force.

## Purpose

Coordinate the WP11-WP14 amendment as a stack of bounded delegated tasks while
one long-lived coordinator preserves the complete decision context, validates
every handoff, owns integration, and reports only evidence-backed completion.

This projection governs execution only. It does not restate or override the
ADR or work-package semantics. When this document and a semantic authority
differ, stop and resolve the conflict in the semantic authority before
implementation continues.

The coordinator remains in the integration worktree. Implementers work in
separate project worktrees and return committed branches plus concise evidence.
Worker conversations are disposable execution context; this coordinating
conversation is the durable reasoning and conformance context.

## Fixed boundaries

- Begin from the exact authority commit above or a coordinator-declared,
  reviewed descendant.
- Retain the completed WP0-WP10 source stack and historical evidence.
- Replace the disposable fixed-client dogfood substrate without compatibility
  aliases, backfills, tombstones, dual reads, or migration theater.
- Do not push, open a pull request, publish a deployment, or disclose a running
  Forge detail without separate human authorization.
- Keep client bootstrap and Work mutation independently disabled by default.
- Do not mark ADR 0004 Accepted or Implemented merely because WP11-WP14 pass.
- Do not claim unavailable MySQL or MSSQL evidence. Those backends are not part
  of this local execution; record them as unproven where the decision requires
  a broader matrix.
- Builders may use at most two non-recursive sub-agents for bounded research or
  tests. The builder owns and inspects their complete contribution.

## Authorities and responsibilities

### Coordinator

The coordinator is the only authority for:

- the released base commit for each package;
- package dispatch and dependency release;
- semantic-conformance decisions across package boundaries;
- the external evidence and finding ledgers;
- complete-diff inspection and integration order;
- aggregate gates and clean-slate dogfood certification; and
- the final statement of complete, partial, blocked, or unproven work.

The coordinator does not absorb a package's implementation context. It reads
the committed diff, tests, review findings, and handoff summary. Semantic
defects return to the owning builder. The coordinator may make only narrow
integration changes whose behavior is already fixed by the authorities.

### Builder

Each builder receives one package, one exact base, explicit ownership, and an
evidence contract. It must:

1. read ADR 0004 and its complete package before editing;
2. inspect the relevant existing implementation and tests;
3. remain inside package ownership and declared integration seams;
4. add focused positive, negative, race, and fault evidence required by the
   package;
5. run the package gate and inspect the complete diff;
6. obtain and close the required opposing-family review;
7. commit with a Conventional Commit and `Assisted-by` trailer; and
8. return the exact commit, parent, commands, outcomes, findings, remaining
   uncertainty, and evidence locations.

A builder must stop and report instead of silently changing ADR 0004, reopening
ADR 0003, weakening authorization or idempotency, adding a compatibility path,
or crossing another package's semantic ownership.

### Reviewer

Independent reviewers are read-only and from an opposing model family. Every
finding is classified as `fix-now`, `prove-with-test`, `document-boundary`,
`separate-decision`, or `decline` with evidence.

Choose review depth by consequence, not by the existence of another edit or
commit. Fable 5 is reserved for security, authorization, credential lineage,
concurrency, atomicity, and comparably consequential cross-boundary decisions.
Routine prose, fixtures, straightforward tests, and mechanical changes use
Opus 5 when independent review is useful, or coordinator judgment when the
existing evidence is sufficient. Test code that changes the meaning of a
security or concurrency proof is assessed by that risk, not its file suffix.

Review the stabilized risk-bearing diff once. A new commit SHA, commit-only
change, or routine test/documentation follow-up does not automatically trigger
another Fable pass. Re-review only the affected obligations when subsequent
changes materially alter risk-bearing behavior or leave a consequential
finding unresolved. Record the reviewed SHA and any later coordinator-checked
delta honestly; never claim an exact-final verdict that was not obtained.

The risk-bearing package gates are:

| Package | Builder family | Review floor |
| --- | --- | --- |
| WP11 | Codex | Fable 5 OAuth, redirect, consent, and abuse review |
| WP12 | Codex | Fable 5 grant, token-lineage, and authority-UX review |
| WP13 | Codex | Opus 5 provenance, privacy, and idempotency review; Fable 5 for unresolved security findings |
| WP14 | Codex | Fable 5 focused final ADR-conformance review |

## Stack and release protocol

The stack is strictly ordered:

```text
8df45e530f  reviewed ADR amendment
    |
    +-- WP11 constrained client bootstrap
            |
            +-- WP12 grant profiles and authority inspection
                    |
                    +-- WP13 required operation attribution
                            |
                            +-- WP14 certification and dogfood evidence
```

WP13 may investigate SDK metadata seams before WP12 completes, but it may not
commit receipt or presentation semantics until the coordinator freezes the
reviewed WP11 and WP12 registration/grant snapshot interfaces.

For every package:

1. The coordinator records the released base SHA.
2. The delegated task creates a dedicated worktree branch from that SHA.
3. The builder implements, tests, reviews, fixes, and commits on that branch.
4. The coordinator verifies the returned parent SHA and clean branch state.
5. The coordinator reads the complete diff and review ledger.
6. The coordinator reruns risk-proportionate gates in the integration tree.
7. Only then does the coordinator integrate the commit and release the new SHA.

No downstream semantic package starts from a merely reported or unreviewed
working tree. A reviewer PASS is evidence, not automatic integration authority.

## Delegated packages

### WP11: constrained MCP client bootstrap

Release directly from the authority commit. The builder owns only WP11 and the
minimum interfaces WP12 and WP13 need. Its final handoff must demonstrate every
WP11 acceptance item, including no authority before consent, closed redirect
classes, PKCE and audience enforcement, provisional expiry and races, bounded
admission, immutable finalized metadata, principal binding, and the removal of
both fixed MCP applications on a fresh database.

The builder must not implement grant-profile replacement, receipt attribution,
or final dogfood certification. Any proposed interface for those packages is
reported explicitly for coordinator review.

### WP12: grant profiles and authority inspection

Release only from reviewed, integrated WP11. The builder owns the grant as the
single authority for the exact MCP profile, atomic profile replacement,
credential-lineage invalidation, reconnect/revoke behavior, and the
principal-facing authority view.

The handoff must prove that failed or denied replacement preserves the old
grant, successful replacement invalidates all old codes and tokens, same-label
installations remain independent, hostile metadata is escaped, and the UI
states only facts Forge owns.

### WP13: operation client attribution

Release only after WP11 and WP12 interfaces are reviewed and frozen. The
builder owns metadata extraction and validation, receipt snapshots, replay
behavior, output schema, and human operation provenance. Attribution remains
client-reported annotation outside authorization and semantic idempotency.

The handoff must prove mutation success with unavailable runtime attribution,
rejection of explicitly malformed standard attribution or supplied model
metadata before receipt lookup or mutation, original attribution source and
labels on replay, and no prompt, request, credential, or hidden-object
retention.

### WP14: amended certification

WP14 begins after WP11-WP13 are reviewed, integrated, and passing their focused
gates. A delegated evidence builder owns bounded conformance tests,
documentation, the proposed dogfood procedure, and the draft evidence matrix.
Semantic fixes return to the owning package branch.

The coordinator independently validates the returned package, tears down the
disposable dogfood runtime and credentials, rebuilds from an empty database,
runs the amended two-installation scenario, captures synthetic human-visible
evidence, completes the disclosure scan, and obtains the final Fable 5 review.
By human direction for this local run, Action execution is not required when no
runner exists; the evidence must say that it was not exercised. This is a
projection-local test boundary, not an ADR semantic exception.

## Handoff schema

Every delegated task returns one compact handoff with:

```text
Package:
Branch:
Base SHA:
Final SHA:
Changed ownership:
Tests and exact outcomes:
Review model and verdict:
Finding ledger:
External evidence:
Known gaps or unproven environments:
Operational-disclosure scan:
Push or PR activity: none
```

The coordinator rejects a handoff when the branch is dirty, the parent is not
the released base, semantic files fall outside ownership without prior
agreement, required evidence is summarized without an exact command or result,
or a review finding is unclassified.

## Coordinator validation gates

### Before dispatch

- Verify the integration tree is clean and the authority commit resolves.
- Record baseline build and focused OAuth/MCP results.
- Create the external package, review, and aggregate evidence ledgers.
- Confirm no running substrate or credential will be treated as migration data.

### After each handoff

- Verify branch, parent, commit trailer, and complete diff.
- Reconfirm that every new migration number is still free on the integration
  head before accepting a migration-bearing handoff.
- Run `git diff --check` and the package's focused tests.
- Run `make fmt` for Go or template changes and inspect formatting fallout.
- Run `make lint-go`; run `make lint-js` for JavaScript or TypeScript changes.
- Verify default-off configuration and absence of operational details.
- Close every opposing-family finding before integration.

### After WP11

Run the original Gate 6 and prove the bootstrap cannot create authority,
escape its closed public-client profile, cross principals, or grow provisional
storage without bound.

### After WP12 and WP13

Run the original Gate 7 and prove one grant authority, complete old-lineage
invalidation, attribution validation before disclosure or mutation, and
original modeled or model-less attribution on semantic replay.

### Final

Run the original Gate 8 on a fresh disposable substrate. Retain only synthetic
labels and reserved example values in repository-safe evidence. Record SQLite
and PostgreSQL results where locally available; mark MySQL and MSSQL unproven
without attempting them. Do not turn a skipped runner action into a passing
claim.

## Final coordinator report

The final report must include:

- the exact authority and integrated stack SHAs;
- WP11-WP14 status and ownership;
- exact local validation commands and outcomes;
- the OAuth abuse, redirect, expiry, and cleanup matrix;
- grant-profile and old-credential invalidation evidence;
- independent two-installation rotation and revocation evidence;
- visible or unavailable runtime attribution and changed-label replay evidence;
- clean-slate reconstruction evidence;
- every opposing-family verdict and classified finding;
- skipped, unavailable, or unproven environments and actions;
- default-off and disclosure-scan confirmation; and
- any remaining condition before ADR acceptance or broader use.

The report must distinguish implemented behavior, observed local behavior, and
inference. It must not rely on worker confidence in place of coordinator
validation.
