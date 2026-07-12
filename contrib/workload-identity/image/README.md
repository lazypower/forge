# Workload identity image

The image uses Gitea's normal root `Dockerfile`. The only packaging additions
are pinned build/runtime base digests and OCI labels identifying the upstream
version/commit and the workload-identity patch revision.

Build locally:

```sh
contrib/workload-identity/image/build.sh
```

The script derives the upstream `v1.26.4` commit, current patch revision, image
version, and `SOURCE_DATE_EPOCH` from Git. Base images are pinned by digest and
BuildKit provenance attestations are disabled because their invocation metadata
would make the manifest digest vary between otherwise identical builds. The
same source, platform, and builder therefore produce the same image digest;
the stable OCI labels carry source provenance. The script prints the local or
published digest. Set `IMAGE_REPOSITORY` or the complete `IMAGE_REF` to choose a
registry and name.

Publishing is an explicit operation and never deploys:

```sh
IMAGE_REPOSITORY=registry.example.net/forge/gitea-workload-identity \
PUSH=1 contrib/workload-identity/image/build.sh
```

Authenticate Docker to the registry first. CI must publish only after all test
gates pass and a human approves the protected publish environment. Deploy the
reported digest, not the mutable tag.

Smoke-test a built or pulled image:

```sh
IMAGE_REF=gitea-workload-identity:1.26.4-wi.<revision> \
contrib/workload-identity/image/smoke.sh
```

The smoke test starts an empty SQLite instance and proves migrations, health,
Actions runner protocol availability, workload discovery/JWKS, unauthorized
issuance denial, and image provenance labels. The Vault acceptance fixture
separately executes complete authorized and unauthorized Actions jobs.
