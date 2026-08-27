# Workload identity maintenance

Forge carries an Actions workload-identity implementation on its immutable
Gitea 1.27.2 foundation. Its lineage is:

1. Gitea issue #26383 and the earlier PR #25664;
2. maintainer-authored draft PR #36988, commit `f24e180a3a4f` by Lunny Xiao;
3. the attribution-preserving import into this repository; and
4. Forge-specific hardening, acceptance, documentation, and packaging commits.

Do not rewrite the imported author's commit or its attribution. New work uses
normal commits and the repository's `Assisted-by` policy.

## Fixed lineage

`contrib/workload-identity/lineage.env` is the single authority for the inherited
foundation:

```text
GITEA_LINEAGE_VERSION=1.27.2
GITEA_LINEAGE_COMMIT=1dac1bb2f8593d4319125fa6bca9283000a2ddc2
```

Those values are provenance, not update inputs. Forge does not fetch, replay,
merge, or rebase onto later Gitea releases. A security or correctness remedy for
retained code is implemented and reviewed as a Forge change.

## Routine operation

There is one supported release path:

```sh
just status
just test
just push
```

Ordinary pushes and pull requests are not release-certified and do not run this
gate. Dependabot pull requests run the narrow, non-certifying dependency gate
documented in `docs/dependency-updates.md`. Contributors and agents report the
specific checks they ran. The full release gate runs only when a human explicitly
prepares a release, and GitHub repeats it for the tagged revision before
publication.

`just status` prints the inherited lineage, current Forge revision, branch, image
repository, and most recently verified revision.

`just test` formats and lints the code, runs focused unit and integration tests,
builds the rootless Linux image twice, verifies reproducibility and runtime
behavior, and executes the Vault acceptance fixture. It writes `fork-health.md`
and binds `verified-image.env` to the exact tested revision.

`just push` refuses dirty or stale results. It creates the next release tag for
the fixed 1.27.2 lineage and pushes the exact verified revision. GitHub Actions
publishes that revision to GHCR, generates an SPDX SBOM, attests provenance and
the SBOM, and attaches machine-readable release metadata. Deployment promotes
the immutable tested digest rather than rebuilding it.

Forge release tags use the `v1.27.2.N` form. The historical `wi-v1.27.2.N` tags
remain intact as provenance, and the tag allocator counts them so the first
Forge-native release continues the existing sequence rather than reusing a
release number.

There is no update or patch-replay command. A clean replay onto a later Gitea
release is not a supported Forge operation.

## Release gate

The release gate must pass all of the following:

- formatting without a resulting diff;
- generated bindata;
- Go lint;
- focused Actions service tests;
- a complete Forge build;
- the SQLite runner integration test;
- two byte-identical rootless image builds;
- the image smoke test; and
- the Vault workload-identity acceptance fixture.

Any failure forbids publication. A passing gate is evidence, not release
authority; human approval is still required.

## Trust-boundary inventory

Files directly responsible for workload identity include:

| File | Invariant |
| --- | --- |
| `models/actions/config.go` | Owner permission defaults and maximum ceiling |
| `models/repo/repo_unit_actions.go` | Repository permission persistence and clamp |
| `services/actions/permission_parser.go` | Workflow/job precedence and `id-token` parsing |
| `services/actions/task.go` | Sole task-context assembly and request credential transport |
| `services/actions/oidc.go` | Eligibility, audience, claims, subject, lifetime, and signing |
| `services/actions/init.go` | Default-off and asymmetric-key startup requirements |
| `routers/api/actions/actions.go` | Workload route registration |
| `routers/api/actions/oidc.go` | Discovery, JWKS, authentication, and response boundary |
| `tests/integration/actions_oidc_test.go` | Real runner-task HTTP contract |
| `modules/setting/actions.go` | Administrator enable switch |
| `custom/conf/app.example.ini` | Documented secure default |

The implementation also depends on inherited authorities for task credentials,
attempt state, cancellation, workflow commits, OAuth signing keys, and canonical
application URLs. Changes in those areas require manual review even when the
workload-identity files themselves do not change.

Conflict or refactoring decisions must preserve these rules:

- Job permissions override workflow permissions; only `id-token: write` allows
  issuance.
- A request credential resolves one exact, currently running task.
- Old, superseded, cancelling, cancelled, and completed attempts fail closed.
- Run repository, workflow source repository, and workflow source commit remain
  distinct authorities.
- Reusable workflow jobs fail closed until caller ancestry is explicit in the
  token contract and covered by negative tests.
- Signing remains asymmetric with a stable `kid` and verifiable JWKS.
- The issuer derives from the canonical `ROOT_URL`, including subpath installs.
- Runner request credentials remain secret-masked.

Every semantic change to this boundary needs a negative test that fails under
the unsafe behavior.

## Rollback

The workload-identity feature adds no database table or column. Its `id-token`
permission uses existing JSON storage, so a rollback within the Forge 1.27.2
lineage is schema-safe.

1. Disable `[actions] WORKLOAD_IDENTITY_ENABLED` and restart if issuance must
   stop immediately.
2. Disable the relying party's JWT role so existing tokens cannot obtain new
   credentials; revoke issued leases when necessary.
3. Wait for the five-minute workload JWT lifetime when practical.
4. Restore the previous Forge image by immutable digest.
5. Verify application health, migrations, login, repository access, and ordinary
   Actions execution. Workflows requesting identity must fail closed.
6. Preserve the rejected candidate's logs, source revision, and image digest for
   diagnosis.

Do not rotate the shared OAuth signing key merely to disable workload identity;
that would also affect human OAuth and OIDC tokens.
