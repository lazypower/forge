#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

repo_root="$(CDPATH= cd -- "$(dirname "$0")/../../.." && pwd)"
cd "$repo_root"

upstream_version="${UPSTREAM_VERSION:-1.26.4}"
upstream_commit="$(git rev-list -n 1 "v$upstream_version")"
patch_revision="${PATCH_REVISION:-$(git rev-parse HEAD)}"
git cat-file -e "$patch_revision^{commit}"
patch_short="$(printf '%.12s' "$patch_revision")"
source_date_epoch="$(git show -s --format=%ct "$patch_revision")"
created="$(git show -s --format=%cI "$patch_revision")"
image_repository="${IMAGE_REPOSITORY:-gitea-workload-identity}"
image_ref="${IMAGE_REF:-$image_repository:$upstream_version-wi.$patch_short}"

set -- docker buildx build \
	--build-arg "GITEA_VERSION=$upstream_version-workload-identity.$patch_short" \
	--build-arg "GITEA_UPSTREAM_VERSION=$upstream_version" \
	--build-arg "GITEA_UPSTREAM_COMMIT=$upstream_commit" \
	--build-arg "GITEA_PATCH_REVISION=$patch_revision" \
	--build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
	--label "org.opencontainers.image.created=$created" \
	--tag "$image_ref"

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
printf 'UPSTREAM_VERSION=%s\n' "$upstream_version"
printf 'UPSTREAM_COMMIT=%s\n' "$upstream_commit"
printf 'PATCH_REVISION=%s\n' "$patch_revision"
