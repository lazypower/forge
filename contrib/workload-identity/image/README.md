# Workload identity image

The image uses the patch-owned rootless Dockerfile in this directory. Its build
and runtime bases are pinned by digest, it runs as `1000:1000`, and its OCI
labels identify the upstream version/commit and workload-identity revision.

Build locally:

```sh
just build
```

The script derives the upstream `v1.27.2` commit, durable patch revision, image
version, and `SOURCE_DATE_EPOCH` from Git. Base images are pinned by digest and
BuildKit provenance attestations are disabled because their invocation metadata
would make the manifest digest vary between otherwise identical builds. The
same source, platform, and builder therefore produce the same image digest;
the stable OCI labels carry source provenance. The script prints the local or
published digest. Set `IMAGE_REPOSITORY` or the complete `IMAGE_REF` to choose a
registry and name.

The default production platform is explicitly `linux/amd64`, matching the
Firecracker runner and deployment hosts even when a build is initiated from an
Arm workstation. Override `TARGET_PLATFORM` only for a separately tested
deployment target. The smoke gate rejects an image whose actual platform does
not match the requested target.

The update gate passes the source branch's patch-tip revision into builds made
from its temporary replay worktree. Image provenance therefore names a durable
commit in the fork, not an ephemeral cherry-pick commit created only for CI.

The security gate builds the candidate twice and requires identical image
references, digests, and patch revisions before smoke or Vault acceptance
testing can authorize publication.

Publishing is an explicit operation and never deploys. Run the full gate, then
push an annotated release tag:

```sh
just test
just push
```

GitHub Actions rebuilds and verifies the tagged revision, publishes it to the
public `ghcr.io/lazypower/gitea-workload-identity` package using its ephemeral
workflow token, emits an SPDX SBOM and release manifest, and creates GitHub and
OCI attestations. Deploy the digest from `release.json`, never the tag.

Smoke-test a built or pulled image:

```sh
IMAGE_REF=gitea-workload-identity:1.27.2-wi.<revision> UPSTREAM_VERSION=1.27.2 \
contrib/workload-identity/image/smoke.sh
```

The smoke test starts an empty SQLite instance and proves migrations, health,
Actions runner protocol availability, workload discovery/JWKS, unauthorized
issuance denial, rootless ownership, durable volume declarations, restart
persistence, and image provenance labels. The Vault acceptance fixture
separately executes complete authorized and unauthorized Actions jobs.
