# Workload identity fork policy

This fork exists only to carry Gitea Actions workload identity until an
acceptable upstream equivalent is available.

It is upstream Gitea plus one intentionally carried security capability, not a
general-purpose fork or downstream distribution. The following are out of
scope:

- unrelated bug fixes;
- UI preferences;
- custom product features;
- general Gitea roadmap work;
- accepting outside feature requests; and
- becoming a downstream distribution.

Do not silently add another desired Gitea feature. Evaluate it separately and
deploy it by another mechanism unless this policy is explicitly changed.

The carried patch may contain only its permission model, issuer and issuance
boundary, claim derivation, runner context, tests, Vault fixture, operator
documentation, and the image/maintenance automation required to operate it.

Upstream compatibility takes priority over stylistic reinvention. Preserve
the lineage of upstream PR #36988 and keep private hardening in small commits.
A future upstream implementation is evaluated for permission gating, exact
live-task authentication, claims, audience/lifetime behavior, and Vault
compatibility. If adequate, migrate Vault policy and configuration, remove the
entire patch series, and return to the stock image. Deleting this fork delta is
success.
