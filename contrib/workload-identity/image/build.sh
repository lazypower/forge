#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

repo_root="$(CDPATH='' cd -- "$(dirname "$0")/../../.." && pwd)"
cd "$repo_root"
. contrib/workload-identity/lineage.env

lineage_version="$GITEA_LINEAGE_VERSION"
lineage_commit="$GITEA_LINEAGE_COMMIT"
git cat-file -e "$lineage_commit^{commit}"
patch_revision="${PATCH_REVISION:-$(git rev-parse HEAD)}"
git cat-file -e "$patch_revision^{commit}"
patch_short="$(printf '%.12s' "$patch_revision")"
source_date_epoch="$(git show -s --format=%ct "$patch_revision")"
created="$(git show -s --format=%cI "$patch_revision")"
image_repository="${IMAGE_REPOSITORY:-forge}"
image_ref="${IMAGE_REF:-$image_repository:$lineage_version-wi.$patch_short}"
target_platform="${TARGET_PLATFORM:-linux/amd64}"

set -- docker buildx build \
	--platform "$target_platform" \
	--file contrib/workload-identity/image/Dockerfile.rootless \
	--build-arg "GITEA_VERSION=$lineage_version-workload-identity.$patch_short" \
	--build-arg "GITEA_UPSTREAM_VERSION=$lineage_version" \
	--build-arg "GITEA_UPSTREAM_COMMIT=$lineage_commit" \
	--build-arg "GITEA_PATCH_REVISION=$patch_revision" \
	--build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
	--label "org.opencontainers.image.created=$created" \
	--tag "$image_ref"

if [ -n "${BUILDX_CACHE_SCOPE:-}" ]; then
	set -- "$@" \
		--cache-from "type=gha,scope=$BUILDX_CACHE_SCOPE" \
		--cache-to "type=gha,mode=max,scope=$BUILDX_CACHE_SCOPE"
fi

if [ "${PUSH:-0}" = 1 ]; then
	set -- "$@" --provenance=false --push
else
	set -- "$@" --provenance=false --load
fi

"$@" .

if [ "${PUSH:-0}" = 1 ]; then
	digest="$(docker buildx imagetools inspect "$image_ref" --format '{{.Manifest.Digest}}')"
else
	digest="$(docker image inspect "$image_ref" --format '{{index .RepoDigests 0}}' 2>/dev/null || true)"
	if [ -z "$digest" ]; then
		digest="local-id:$(docker image inspect "$image_ref" --format '{{.Id}}')"
	fi
fi

printf 'IMAGE_REF=%s\n' "$image_ref"
printf 'IMAGE_DIGEST=%s\n' "$digest"
printf 'LINEAGE_VERSION=%s\n' "$lineage_version"
printf 'LINEAGE_COMMIT=%s\n' "$lineage_commit"
printf 'PATCH_REVISION=%s\n' "$patch_revision"
printf 'TARGET_PLATFORM=%s\n' "$target_platform"
