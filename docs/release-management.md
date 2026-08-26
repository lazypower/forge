# Release management

Forge releases exist to serve the maintained deployment. They do not follow
Gitea's release calendar, branch structure, version support window, or backport
policy.

## Release authority

The Forge maintainer decides when a release is needed and which commit it
contains. A release candidate must identify its exact source commit and pass the
tests and operational checks relevant to the changed behavior.

## Versioning

Until Forge adopts its own stable version scheme, tags may describe the inherited
Gitea compatibility baseline and a Forge-specific revision. Such a tag records
compatibility; it does not make the release part of Gitea's release line.

Once Forge publishes a native versioning policy, that policy becomes the sole
authority for later releases. No Gitea version is automatically supported merely
because it exists upstream.

## Fixes and support

Fixes are made on the current development line unless the maintained deployment
requires a narrowly scoped release branch. Forge does not automatically backport,
frontport, or ingest upstream changes.

Only the versions described in [SECURITY.md](../SECURITY.md) are eligible for
security fixes. Every release should preserve the license and provenance material
described in [PROVENANCE.md](../PROVENANCE.md).
