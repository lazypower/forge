# Project governance

Forge is a personal project with one maintainer. This document makes that
authority explicit so inherited Gitea governance is not mistaken for Forge's.

## Authority

The maintainer listed in [MAINTAINERS](../MAINTAINERS) owns product direction,
security decisions, releases, repository access, and the final decision to
accept or reject a contribution. There is no technical oversight committee,
maintainer election, merge quorum, or guarantee that a proposal will be
accepted.

Gitea is the provenance of the inherited source, not a governance authority for
Forge. Forge does not preserve compatibility or structure in anticipation of
later Gitea changes.

## Review

Changes should protect repository integrity, upgrade and migration safety,
security boundaries, and the behavior relied on by the maintained deployment.
Review should distinguish required corrections from optional suggestions and
record important product or architectural decisions in the repository.

Software agents may implement, test, and review changes. Agent output is
evidence, not authority: the maintainer remains accountable for accepting the
result.

## Commits and releases

Commit messages and pull request titles use Conventional Commits. Material agent
assistance is recorded with the repository's required `Assisted-by` trailer;
agents do not add `Co-Authored-By` or `Signed-off-by` trailers.

Releases are cut when the maintained deployment has a verified need and the
candidate satisfies the relevant tests and operational checks. Forge does not
follow Gitea's release calendar or versioning decisions.
