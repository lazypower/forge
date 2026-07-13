# Workload identity maintenance runbook

This runbook applies to the private workload-identity patch based on Gitea
`v1.26.4`. Its patch lineage is:

1. Gitea issue #26383 and the earlier PR #25664;
2. maintainer-authored draft PR #36988, commit `f24e180a3a4f` by Lunny Xiao;
3. the attribution-preserving import on this branch; and
4. private hardening, acceptance, documentation, and packaging commits.

Do not rewrite the imported author's commit. New maintenance work uses normal
commits and preserves the local `Assisted-by` policy.

## Upgrade procedure

1. Record the deployed image digest, upstream version, patch revision, and
   database backup identifier.
2. Fetch the candidate upstream release tag. Read its release notes and inspect
   every trust-boundary path below, even if Git reports no conflict.
3. Create an update branch from the candidate tag.
4. Replay the ordered patch commits with `git cherry-pick`. Record every commit
   result and conflict path. Never force-push or silently drop a hunk.
5. Compare upstream for an equivalent Actions OIDC implementation. If one
   exists, stop the mechanical rebase and perform the convergence review below.
6. Resolve conflicts according to the owning invariant, not by choosing “ours”
   or “theirs” wholesale.
7. Run formatting, unit tests, the official SQLite integration test, image
   smoke test, and Vault acceptance fixture.
8. Build the candidate image once, record its immutable digest and provenance,
   and promote that same digest after human review. Do not rebuild between test
   and promotion.
9. Back up the database and signing key, deploy to a canary if available, then
   verify discovery, JWKS, a denied unauthenticated request, one authorized
   Vault exchange, and normal Actions execution.
10. Retain the previous image digest and backup until the observation window is
    complete.

A clean cherry-pick is not security evidence. Mark the update high risk and
require manual review whenever any trust-boundary file changes upstream.

## Automated update gate

`contrib/workload-identity/upstream.env` is the single authority for the
deployed patch base and candidate upstream release. Renovate updates only the
candidate value when `go-gitea/gitea` publishes a release. Its pull request is
always labeled for manual review.

The `workload identity security gate` workflow runs on every change and weekly.
It produces `patch-health.md`, which records the old and candidate versions,
each replayed commit, conflicts, trust-boundary changes, possible equivalent
upstream implementations, every validation result, and the tested image digest.
Any failed gate forbids publication.

To prepare an update locally without running the full suite:

```sh
UPDATE_WORKTREE=/tmp/gitea-workload-identity-update \
  contrib/workload-identity/ci/prepare-update.sh
```

Run the complete gate with:

```sh
contrib/workload-identity/ci/verify-update.sh
```

The scheduled upstream-tracking workflow runs pinned Renovate weekly. Configure
the repository secret `RENOVATE_TOKEN` with a dedicated Gitea token permitted
to read this repository, push update branches, and open pull requests. Configure
the optional `RENOVATE_GITHUB_TOKEN` secret to avoid anonymous GitHub release
API rate limits. The Renovate account must not merge or deploy changes.

Configure the Gitea Actions environment `workload-identity-publish` with
required human approvers and registry secrets
`WORKLOAD_IDENTITY_REGISTRY_USERNAME` and
`WORKLOAD_IDENTITY_REGISTRY_PASSWORD`. Only a manually dispatched run with an
explicit repository can enter that environment. Publication pushes the exact
locally tested image; it does not deploy it.

## Conflict rules

- Permission parser changes must preserve explicit job-over-workflow precedence
  and `id-token: write`; absence, `none`, and `read` deny.
- Task credential changes must still resolve one exact credential in constant
  time and reload current server state. Never trust IDs from the request.
- Attempt/rerun changes must identify the newest running task and reject old,
  superseded, cancelling, cancelled, and completed work.
- Workflow model changes must derive source repository, source commit, and
  reusable ancestry explicitly. Do not keep the 1.26.4 same-repository
  assumption after reusable/scoped workflows appear.
- OAuth signer changes must retain an asymmetric public key, stable `kid`, and
  JWKS verification. Review rotation semantics.
- URL/router changes must preserve the distinct path issuer derived from
  canonical `ROOT_URL`, including subpath installations.
- Runner context changes must keep the request credential secret-masked and
  must not replace the exact-task authentication check with a caller identity.

After resolving a semantic conflict, add or update a negative test that would
fail under the incorrect resolution.

## Trust-boundary inventory

Patch-owned or directly modified upstream files:

| File | Dependency/invariant |
| --- | --- |
| `models/actions/config.go` | owner permission defaults and maximum ceiling |
| `models/repo/repo_unit_actions.go` | repository permission persistence and clamp |
| `services/actions/permission_parser.go` | workflow/job YAML precedence and `id-token` parsing |
| `services/actions/permission_parser_test.go` | permission regression evidence |
| `services/actions/task.go` | sole task-context assembly and request credential transport |
| `services/actions/oidc.go` | task eligibility, audience, claims, subject, lifetime, signing |
| `services/actions/oidc_test.go` | lifecycle and authorization adversarial coverage |
| `services/actions/init.go` | default-off/asymmetric startup invariant |
| `routers/api/actions/actions.go` | workload route registration |
| `routers/api/actions/oidc.go` | discovery, JWKS, HTTP authentication and response boundary |
| `tests/integration/actions_oidc_test.go` | real runner-task HTTP contract |
| `modules/setting/actions.go` | administrator enable switch |
| `custom/conf/app.example.ini` | documented secure default |

Semantically depended-on upstream authorities, even when not modified:

| File/area | Question it owns |
| --- | --- |
| `models/actions/task.go` | Which raw credential maps to a running task? |
| `models/actions/run_job.go` | Which task and attempt are current for the job? |
| `models/actions/run.go` | What run, ref, SHA, event, actor, and status are authoritative? |
| `services/actions/run.go`, `services/actions/job_emitter.go` | How are permissions persisted and attempts scheduled? |
| `services/actions/rerun.go` | How are old jobs/tasks superseded? |
| `models/actions/run.go:CancelJobs`, `models/actions/task.go:StopTask` | When does cancellation become non-running? |
| `modules/actions/workflows.go` | Which workflow directory/path exists at the run commit? |
| `modules/git`, `modules/gitrepo` | How is the immutable workflow commit resolved? |
| `services/oauth2_provider/jwtsigningkey.go` | Which key signs and which JWK/algorithm is published? |
| `modules/setting/server.go`, application URL settings | What canonical `ROOT_URL` produces issuer URLs? |
| Gitea Runner task-context mapping | How are request URL/token exported and masked? |

Changes in Actions permissions, task tokens, status transitions, attempts,
reruns, cancellation, workflow directories, reusable/scoped workflows, OAuth
signing, routing, or canonical URL handling require manual review.

## Rollback

The patch adds no database table or column. `id-token` is stored in existing
JSON and stock Gitea ignores the extra JSON field, so rollback to the prior
known-good `v1.26.4` image is schema-safe.

1. Disable `[actions] WORKLOAD_IDENTITY_ENABLED` and restart if immediate
   issuance shutdown is needed.
2. Revoke or disable the Vault JWT role so already issued JWTs cannot obtain new
   Vault credentials. Revoke existing Vault leases if the incident requires it.
3. Wait five minutes for workload JWT expiry when practical.
4. Restore the prior image by immutable digest. Do not move a mutable tag.
5. If the candidate ran migrations from a newer upstream release, follow that
   release's supported database rollback procedure or restore the pre-upgrade
   backup; the workload patch itself has no migration.
6. Verify Gitea health, migrations, login, repository access, and ordinary
   Actions. Workflows requesting identity should now fail closed.
7. Preserve candidate logs and digest for diagnosis.

Do not delete or rotate the shared OAuth signing key merely to disable workload
identity; that also affects human OAuth/OIDC tokens.

## Upstream convergence

For every update, check whether upstream provides a workload issuer, compatible
token request contract, explicit permission gating, exact live-task checks,
the required claims, a short audience-bound token, and Vault-compatible JWKS.

If it does:

1. run both implementations against the same acceptance fixture;
2. compare issuer, subject, claim types, audience, lifetime, task lifecycle, and
   runner behavior;
3. update Vault roles to the upstream contract and test denial cases;
4. deploy stock Gitea with workload identity still gated;
5. remove the imported and private patch commits plus their now-obsolete build
   automation; and
6. retain only migration/operational history.

Do not preserve private compatibility aliases without an explicit need. The
preferred end state is a stock image with no carried patch.
