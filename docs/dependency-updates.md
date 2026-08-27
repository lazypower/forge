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

Every verdict names the ecosystem and update class, the matched policy rule,
the reason for the decision, the risk being controlled, the changed files, and
the scrutiny required to clear a hold. The pull request title is displayed as
context but is not trusted as policy input.

The gate compares the current base with GitHub's proposed merge commit and runs
candidate checks against that merged result. A dependency branch that predates
new commits on `main` therefore does not inherit unrelated changes in its diff.

## Policy map

The branch rule comes from `.github/dependabot.yml`; the classifier in
`tools/dependency-verdict.sh` is the executable authority for these decisions.

| Dependabot rule | Classification | Result and reason |
| --- | --- | --- |
| `automerge-actions-patches` | Actions patch | Candidate only when every change is a SHA-pinned `uses:` substitution. |
| `automerge-go-patches` | Direct Go patch | Candidate only when the merge changes `go.mod` and `go.sum`. |
| `automerge-frontend-patches` | Direct npm patch | Candidate only when the merge changes `package.json` and `pnpm-lock.yaml`. |
| `review-actions-minors` | Actions minor | Hold because runner behavior, runtimes, inputs, outputs, or permissions may change. |
| `review-go-minors` | Direct Go minor | Hold because APIs, defaults, generated behavior, transitive dependencies, or platforms may change. |
| `review-frontend-minors` | Direct npm minor | Hold because browser behavior, build output, plugin contracts, defaults, or transitive packages may change. |
| `review-image-updates` | Container patch or minor | Hold because the operating-system graph, entrypoint, architecture support, or runtime behavior may change. |
| Any ungrouped update | Unproven scope | Hold because its branch does not prove patch-only eligibility; this includes major and security-specific updates. |

Any candidate that changes files beyond its allowed dependency surface becomes
a hold, with the unexpected files named in the annotation and summary.

## Runner budget

Every Dependabot pull request pays first for one checkout, a sub-second policy
self-test, and a diff-only risk classification. Held updates stop there.

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
