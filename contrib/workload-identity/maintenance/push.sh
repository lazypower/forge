#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

remote="${1:-github}"
repo_root="$(CDPATH='' cd -- "$(dirname "$0")/../../.." && pwd)"
cd "$repo_root"
. contrib/workload-identity/lineage.env

test -z "$(git status --porcelain)" || { echo 'working tree must be clean' >&2; exit 1; }
if ! test -f fork-health.md || ! grep -q '^All gates passed\.' fork-health.md; then
	echo 'just test has not passed' >&2
	exit 1
fi
. ./verified-image.env
test "$PATCH_REVISION" = "$(git rev-parse HEAD)" || {
	echo 'verification result is stale; run just test' >&2
	exit 1
}

prefix="wi-v$GITEA_LINEAGE_VERSION."
revision=1
while git rev-parse --verify --quiet "refs/tags/$prefix$revision" >/dev/null; do
	revision=$((revision + 1))
done
tag="$prefix$revision"
git tag --annotate "$tag" --message "Forge lineage $GITEA_LINEAGE_VERSION release $revision"
git push "$remote" HEAD "refs/tags/$tag"
printf 'published source tag %s; GitHub Actions now builds and attests the GHCR image\n' "$tag"
