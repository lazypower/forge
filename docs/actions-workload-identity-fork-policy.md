# Workload identity lineage and scope

Forge began as a narrowly maintained Gitea fork carrying one Actions workload
identity capability. That former project-wide scope was superseded when Forge
became an independent product; the [root README](../README.md) is authoritative
for current project direction. The original restrictions are retained here as
historical context:

- unrelated bug fixes;
- UI preferences;
- custom product features;
- general Gitea roadmap work;
- accepting outside feature requests; and
- becoming a downstream distribution.

They no longer restrict Forge development. Material product and architectural
changes are instead governed by the maintainer and recorded in the repository.
See the [architecture decision log](architecture/decisions/README.md).

Within the workload identity capability, preserve its focused security boundary:
permission model, issuer and issuance boundary, claim derivation, runner context,
tests, Vault fixture, operator documentation, and the image and maintenance
automation required to operate it.

Preserve the lineage of Gitea PR #36988 where it remains useful. A later Gitea
implementation may be consulted for security or correctness, but it is not a
product authority and does not create an obligation to replace Forge's
implementation or return to a stock Gitea image.
