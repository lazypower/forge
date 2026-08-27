# Dependency updates

Forge treats dependency automation as a risk classifier, not as release
certification. The `dependency / merge signal` check is the single repository
signal for Dependabot pull requests.

## Reading the signal

- Green means the update belongs to a patch-only `automerge-*` group, changes
  only that ecosystem's manifests, and passes its focused compatibility checks.
  Classification uses Dependabot's bot-created head branch, not the editable
  pull request title.
- Red with `Dependency review required` means the update is intentionally held.
  This includes every major update, every minor update, every container update,
  mixed file changes, and anything the classifier does not recognize.
- Red from a later step means an otherwise low-risk patch failed a relevant
  integrity, advisory, compile, lint, test, or build check.

The gate does not merge anything. It establishes the evidence that a later
automerge mechanism can consume without duplicating dependency policy.

## Runner budget

Every Dependabot pull request pays first for one checkout and a diff-only risk
classification. Held updates stop there.

| Candidate | Checks that pay rent |
| --- | --- |
| GitHub Action patch | Only pinned `uses:` lines may change; validate the repository workflow inventory. |
| Go module patch | Verify and tidy-check the module graph, then compile production and unit-test surfaces without running tests. |
| Frontend patch | Reject newly introduced high or critical production advisories, install without lifecycle scripts, lint, run unit tests, and build the frontend bundle. |

Container updates and all minor or major updates require human review because a
cheap generic check cannot establish their runtime compatibility. A reviewer
can run focused tests justified by the dependency's actual reach.

## npm trust boundary

`pnpm-workspace.yaml` applies a seven-day release quarantine to direct and
transitive packages and rejects a release whose publisher trust evidence is
weaker than earlier releases. The committed `allowBuilds` map remains the sole
authority for package lifecycle scripts. The dependency gate additionally
installs patch candidates with lifecycle scripts disabled.

Exact-version `trustPolicyExclude` entries grandfather pre-policy lockfile debt.
They must not use ranges: a later release must prove that its publisher trust
has not regressed.

The audit comparison is a delta gate: existing advisories do not conceal a new
high or critical production advisory, and existing debt does not make every
unrelated patch permanently red.
