# ADR 0004 WP10 local certification

**Date:** 2026-08-29

**Integration parent:** `ea523caf8734fe541ae88eb74bda593ee2a32ea6`

**Scope:** locally usable, image-ready, off-by-default Work dogfood slice

This ledger records local implementation evidence, not approval for production
enablement. ADR 0003 and ADR 0004 remain Proposed. The MCP endpoint, Work
inspection, Work mutation, and every Project's planning state remain disabled
by default. The final candidate commit and opposing-family review verdict belong
in the immutable handoff for this branch so this file does not point at itself.

## Work-package status

| Package | Local status | Evidence boundary |
| --- | --- | --- |
| WP0 dependency authority | Complete | Native dependency writes, rollback, concurrency, permissions, and human/REST convergence pass on the supported local matrix. |
| WP1 planning state and bounds | Complete | Migration v344 preserves ordinary Projects as `disabled`; settings are limits, not enablement. |
| WP2 native Work projection | Complete | Side-effect-free native composition, permission filtering, pagination, query-count, capacity, cancellation, and output bounds pass. |
| WP3 shared mutation envelope | Complete | Guarded mutations, serializable lock order, rollback, and post-commit separation pass. |
| WP4 atomic plan mutations | Complete for local dogfood | Atomic revisions, lifecycle guards, replay suppression, and post-commit ordering pass. Crash-safe delivery remains a separate prerequisite below. |
| WP5 human planning surfaces | Complete | Focused unit, integration, template, and two-browser lifecycle evidence passes while ordinary controls remain available. |
| WP6 OAuth write profile | Complete | Exact scopes, audience, PKCE, explicit consent, REST isolation, refresh, and feature-gated issuance pass. |
| WP7 read MCP tools | Complete | Official SDK schemas, permission-neutral discovery, bounded output, timeout, cancellation, and capacity pass. |
| WP8 receipts and provenance | Complete | Canonical replay, ambiguity recovery, rollback, native links, retirement, and no-secret persistence pass. |
| WP9 mutation MCP tools | Complete | An official Go SDK harness exercises all three write schemas and the shared mutation envelope; this is protocol evidence, not production-client bridge interoperability. |
| WP10 rollout and certification | Complete for local dogfood | Documentation, SQLite/PostgreSQL gates, the two-run scenario, disclosure checks, and immutable opposing-family review are required by the final handoff. Broad rollout remains blocked by the prerequisite below and ADR acceptance. |

## Configuration and compatibility

| Claim | Result | Evidence |
| --- | --- | --- |
| Endpoint and both Work layers default off | Passed | Production-default discovery tests and `custom/conf/app.example.ini`. |
| Existing pull inspection remains available with Work flags off | Passed | Discovery coverage proves both Work flags off advertises pull inspection only; the dogfood also disables mutation and calls `pull_request.inspect` with the official client. |
| Read OAuth cannot mutate | Passed | Read inspection succeeds and the same profile receives a permission-neutral mutation rejection. |
| PAT remains read-only | Passed | Integration discovery and official-client PAT coverage. |
| Explicit Project opt-in | Passed | Migration and lifecycle tests prove ordinary Projects stay `disabled` until a guarded begin operation. |
| Disabled/read-only rollback preserves ADR 0002 behavior | Passed | Feature discovery plus live pull inspection after Work mutation is disabled. No rollback deletes Project state or receipts. |
| Old-image rollback | Documented boundary | Migrations v344/v345 are additive, but an older image on the upgraded schema is not claimed compatible. Restore a tested pre-upgrade database backup. |

## Database and migration matrix

| Backend | v344 to v345 | Affected race/integration suite | Two-run dogfood | Result |
| --- | --- | --- | --- | --- |
| SQLite | Focused sequential upgrade passed | Passed | Passed | `passed` |
| Disposable isolated PostgreSQL | Focused sequential upgrade passed | Passed | Passed | `passed` |
| MySQL | Not run | Not run | Not run | `not tested by project decision` |
| MSSQL | Not run | Not run | Not run | `not tested by project decision` |

The focused sequential migration test inserts an ordinary pre-v344 Project,
applies v344 and v345, proves the Project remains `disabled`, and verifies the
three narrow receipt/link tables. The full historical SQLite migration harness
also reached v344 and v345, but its later synchronization of the legacy 1.7.0
fixture failed because that fixture's OAuth authorization-code table lacks the
modern primary-key column. That historical fixture result is not reported as a
pass and does not replace the focused v344-to-v345 evidence.

Only disposable test databases were used. No configured or potentially
non-test database was targeted.

## Security and fault matrix

| Boundary | Result | Focused authority |
| --- | --- | --- |
| Anonymous credential | Passed | MCP authentication and transport-boundary tests reject before resource disclosure. |
| Read OAuth | Passed | Official-client dogfood inspection succeeds; mutation is not authorized. |
| Write OAuth | Passed | Exact write profile drives all three mutation schemas and the full dogfood lifecycle. |
| PAT | Passed | Official-client read compatibility passes; mutation discovery excludes PAT. |
| Wrong audience | Passed | OAuth verification and MCP profile tests reject alternate audience representations. |
| Missing repository/unit permission | Passed | Native mutation, projection, OAuth, and integration permission suites fail closed. |
| Hidden dependency | Passed | Plan inspection with an unreadable prerequisite fails closed without identifying it. |
| Archived repository | Passed | Mutation rejects while archived and recomposes from native state after unarchive. |
| Disabled unit | Passed | Native Work permission/unit guards reject before mutation. |
| Stale request/token | Passed | Item content-version and plan-token tests leave native state unchanged. |
| Duplicate idempotency key | Passed | Identical replay converges; changed content returns non-disclosing `idempotency_conflict`. |
| Ambiguous commit/response loss | Passed | Receipt recovery returns the committed operation; definitely absent and unknown outcomes remain distinct. |
| Timeout and cancellation | Passed | Reads, mutations, endpoint execution, and database cancellation boundaries pass. |
| Capacity | Passed | One non-blocking endpoint capacity authority spans pull and Work tools and recovers after failure. |
| Query count | Passed | Native plan reads batch by set and remain independent of plan width. |
| Output bound | Passed | Read and mutation schemas reject oversized semantic output without undoing a committed native mutation. |
| Receipt contents | Passed | Schema, service, and dogfood checks exclude raw credentials, raw keys, request bodies, copied Work state, and copied readiness. |

## Twelve-step dogfood

One synthetic repository, principal, OAuth client, and Project were used. The
second mutation series reused the first run's exact idempotency keys.

| Step | First run | Identical replay |
| --- | --- | --- |
| 1. Read profile | Plan inspection succeeded; mutation was rejected without disclosure. | Read behavior remained unchanged. |
| 2. Write profile | Exact write OAuth profile issued only after explicit consent. | The same consented authority envelope authorized every replayed mutation. |
| 3. Begin draft | One native repository Project entered `draft`. | Same operation identity returned with `replayed=true`. |
| 4. Bounded revision | Three native Issues, three memberships, and a two-edge chain were created atomically. | Same operation identity returned; no native row count changed. |
| 5. Revision replay | Immediate replay left every native-fact and narrow-receipt count unchanged. | Full-series replay retained the same zero-delta result. |
| 6. Changed request, same key | `idempotency_conflict` disclosed no earlier target. | The same conflict recurred without new state. |
| 7. Cycle | The complete cycle attempt rolled back. | The same rejected outcome returned without new state. |
| 8. Activate | Current just-in-time token activated the Project and composed the first ready Issue. | Same activation operation replayed without new state. |
| 9. Stale activation | An older token with the current expected state returned `conflict`; planning state did not change. | Same rejection replayed without new state. |
| 10. Close ready Issue | Native Issue close made the next dependent ready on reinspection. | Same close operation replayed without new state. |
| 11. Human surface | Project state, ready context, and honest unverified-software provenance matched MCP. | The human page was fetched again and still matched the post-replay MCP view. |
| 12. Disable mutation | Write tools and the issuable write profile disappeared; existing pull inspection still succeeded. | Disabled discovery remained pull-compatible. |

The scenario additionally snapshots native Project, Issue, membership,
dependency, comment, receipt, artifact-link, and event-link counts and asserts
that both the immediate revision replay and the complete mutation-series replay
leave them unchanged. It stores no projection, raw credential, raw idempotency
key, or request body, and it makes no execution or verified-actor claim.

## Certification gate outcomes

The final local source tree produced these results. PostgreSQL commands used a
dedicated disposable environment; connection values are intentionally omitted.

| Gate | Command or scope | Outcome |
| --- | --- | --- |
| Format | `GOCACHE=/tmp/codex-wp10-gocache make fmt` | Passed; an unrelated pre-existing formatter drift was excluded from the WP10 diff. |
| Go lint | `GOCACHE=/tmp/codex-wp10-gocache make lint-go` | Tool failure: Go 1.27.0 caused `golangci-lint` v2.12.2 / `honnef.co/go/tools` v0.7.0 `buildir` to panic on `*ast.KeyValueExpr`; dependent nilness/purity analyzers then lacked IR. No source diagnostic was reported. |
| JavaScript/TypeScript lint | `make lint-js` | Passed: ESLint and `vue-tsc`. |
| Backend | `GOCACHE=/tmp/codex-wp10-gocache make backend` | Passed. |
| Affected Go packages | `go test -count=1 -race` across the affected model, setting, service, OAuth, MCP router, and repository-web packages | Passed. |
| Focused migrations | `go test -count=1 -race ./models/migrations/v1_27` with the three v344/v345 tests selected | Passed on SQLite and disposable PostgreSQL. |
| Interface/domain integration | Focused 25-test `tests/integration` race selection covering transactions, permissions, OAuth, MCP, human symmetry, and dogfood | Passed on SQLite and disposable PostgreSQL. |
| Dogfood | `make 'test-integration#TestMCPWorkPlanningDogfoodWithOfficialClient'` with race enabled | The official Go SDK protocol harness passed independently on SQLite and disposable PostgreSQL; no production-client bridge was tested. |
| Browser symmetry | `GITEA_TEST_E2E_FLAGS='tests/e2e/issue-project.test.ts --grep "work planning lifecycle preserves ordinary project controls"' make test-e2e` | Passed in Chromium and Firefox. |
| Markdown | `pnpm exec markdownlint` over ADRs 0001-0004, ADR 0004 plans, and `docs/mcp.md` | Passed. |
| Mermaid | Parse every Mermaid fence in ADRs 0001-0004 and their plans with the repository's Mermaid 11.16.1 dependency | Passed: 12 diagrams. |
| Local links | Verify relative Markdown targets in the same implementation set | Passed: 138 targets. |
| Whitespace | `git diff --check` | Passed. |

The Go lint crash is a Go 1.27 analyzer incompatibility. Formatting,
compilation, race tests, integration, and both database runs provide independent
source evidence; the analyzer crash is retained as a toolchain exception rather
than relabeled as a pass.

## Remaining prerequisite and non-goals

WP4 proves that notifications, webhooks, indexing, and receipt effects happen
only after commit and are suppressed on replay. It does not provide durable
acknowledgement across a crash between commit and synchronous process-local
fanout. The
[WP4 post-commit delivery prerequisite](0004-wp4-post-commit-delivery-prerequisite.md)
must be satisfied before broad rollout or a deployment that requires
crash-safe at-least-once effects.

Ready-work notification delivery and a general durable outbox are not part of
this local finish line. Neither are a persisted Work projection, generic batch
language, agent or execution lifecycle, cross-repository planning mutation, or
a verified software-actor claim. ADR 0003 and ADR 0004 must remain Proposed
until their decision dependencies and full acceptance evidence are complete.
