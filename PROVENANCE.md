# Source provenance

Forge is an independent derivative of Gitea. This document records the source
boundary so the inherited work and Forge's later work are not confused.

## Inherited source

- Project: Gitea
- Source repository: <https://github.com/go-gitea/gitea>
- Release: `v1.27.2`
- Source commit: `1dac1bb2f8593d4319125fa6bca9283000a2ddc2`
- Source release date: 2026-08-13

The Forge lineage was declared from project commit
`82133b30cd2f26621848c69aa856c48b3ec99127`, which includes the inherited Gitea
source and the project's initial workload-identity changes.

Forge does not track Gitea after this source boundary. Later similarities may
come from shared ancestry, independently selected fixes, protocol compatibility,
or independent implementation. They do not imply an ongoing upstream
relationship.

## Copyright and licenses

The inherited source remains subject to its existing copyright notices and the
MIT license in [LICENSE](LICENSE). In particular, Forge does not claim authorship
of work credited to the Gitea Authors, the Gogs Authors, or third-party
contributors.

Third-party Go and frontend dependencies retain their respective licenses.
Forge preserves the inherited machinery that collects those notices into the
`licenses.txt` file emitted by production builds.

## Names and marks

Gitea and its visual identity belong to their respective owners. Forge uses the
name Gitea only where needed to describe its origin or current implementation
compatibility. Forge is not affiliated with or endorsed by the Gitea project or
Gitea Limited.
