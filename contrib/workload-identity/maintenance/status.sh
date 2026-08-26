#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

repo_root="$(CDPATH='' cd -- "$(dirname "$0")/../../.." && pwd)"
cd "$repo_root"
. contrib/workload-identity/lineage.env

printf 'lineage:  v%s (%s)\n' "$GITEA_LINEAGE_VERSION" "$GITEA_LINEAGE_COMMIT"
printf 'fork:     %s\n' "$(git rev-parse HEAD)"
printf 'branch:   %s\n' "$(git branch --show-current)"
printf 'image:    ghcr.io/lazypower/forge\n'
if [ -f verified-image.env ]; then
	. ./verified-image.env
	printf 'verified: %s (%s)\n' "$IMAGE_REF" "$PATCH_REVISION"
else
	printf 'verified: no; run just test\n'
fi
