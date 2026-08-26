#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

repo_root="$(CDPATH='' cd -- "$(dirname "$0")/../../.." && pwd)"
cd "$repo_root"
. contrib/workload-identity/upstream.env

printf 'upstream: v%s\n' "$GITEA_PATCH_BASE_VERSION"
printf 'patch:    %s\n' "$(git rev-parse HEAD)"
printf 'branch:   %s\n' "$(git branch --show-current)"
printf 'image:    ghcr.io/lazypower/gitea-workload-identity\n'
if [ -f verified-image.env ]; then
	. ./verified-image.env
	printf 'verified: %s (%s)\n' "$IMAGE_REF" "$PATCH_REVISION"
else
	printf 'verified: no; run just test\n'
fi
