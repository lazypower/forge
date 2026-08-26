#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

version="${1:?usage: update.sh VERSION}"
case "$version" in *[!0-9A-Za-z.-]*|'') echo "invalid Gitea version: $version" >&2; exit 2 ;; esac

repo_root="$(CDPATH='' cd -- "$(dirname "$0")/../../.." && pwd)"
cd "$repo_root"
. contrib/workload-identity/upstream.env

test -z "$(git status --porcelain)" || {
	echo 'working tree must be clean' >&2
	exit 1
}
test "$version" != "$GITEA_PATCH_BASE_VERSION" || {
	echo "already based on v$version" >&2
	exit 1
}

git fetch --no-tags "$GITEA_UPSTREAM_REPOSITORY" "refs/tags/v$version:refs/tags/v$version"
git rev-parse --verify "v$version^{commit}" >/dev/null

old_tip="$(git rev-parse HEAD)"
old_base="v$GITEA_PATCH_BASE_VERSION"
branch="codex/workload-identity-$version"
git show-ref --verify --quiet "refs/heads/$branch" && {
	echo "branch already exists: $branch" >&2
	exit 1
}

git switch --create "$branch" "v$version"
set --
for commit in $(git rev-list --reverse "$old_base..$old_tip"); do
	set -- "$@" "$commit"
done
if ! git cherry-pick "$@"; then
	cat >&2 <<EOF
Patch replay stopped at a conflict. Resolve it, run the focused tests for the
affected trust boundary, then continue with: git cherry-pick --continue
EOF
	exit 1
fi

sed "s/^GITEA_CANDIDATE_VERSION=.*/GITEA_CANDIDATE_VERSION=$version/; s/^GITEA_PATCH_BASE_VERSION=.*/GITEA_PATCH_BASE_VERSION=$version/" \
	contrib/workload-identity/upstream.env > contrib/workload-identity/upstream.env.next
mv contrib/workload-identity/upstream.env.next contrib/workload-identity/upstream.env
git add -- contrib/workload-identity/upstream.env
git commit -m "chore(actions): transition workload identity to $version" \
	-m "Assisted-by: ${ASSISTED_BY:-Codex:gpt-5.6}"

cat <<EOF
Patch replayed onto v$version on $branch.

Next:
  1. Review: git log --oneline v$version..HEAD
  2. Read upstream release notes and inspect trust-boundary changes.
  3. Run: just test
  4. Run: just push
EOF
