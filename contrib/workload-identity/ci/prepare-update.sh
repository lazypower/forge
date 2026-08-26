#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

repo_root="$(CDPATH='' cd -- "$(dirname "$0")/../../.." && pwd)"
report="${PATCH_HEALTH_REPORT:-$repo_root/patch-health.md}"
target="${UPDATE_WORKTREE:?set UPDATE_WORKTREE to an unused directory}"

# shellcheck disable=SC1091
. "$repo_root/contrib/workload-identity/upstream.env"

case "$GITEA_PATCH_BASE_VERSION:$GITEA_CANDIDATE_VERSION" in
	*[!0-9A-Za-z.:-]*) echo "invalid upstream version" >&2; exit 2 ;;
esac

base_tag="v$GITEA_PATCH_BASE_VERSION"
candidate_tag="v$GITEA_CANDIDATE_VERSION"
git -C "$repo_root" rev-parse --verify "$base_tag^{commit}" >/dev/null
if ! git -C "$repo_root" rev-parse --verify "$candidate_tag^{commit}" >/dev/null 2>&1; then
	git -C "$repo_root" fetch --no-tags "$GITEA_UPSTREAM_REPOSITORY" "refs/tags/$candidate_tag:refs/tags/$candidate_tag"
fi

patch_tip="${PATCH_TIP:-HEAD}"
if [ "$GITEA_PATCH_BASE_VERSION" != "$GITEA_CANDIDATE_VERSION" ] && [ "$patch_tip" = HEAD ]; then
	changed="$(git -C "$repo_root" diff-tree --no-commit-id --name-only -r HEAD)"
	if [ "$changed" = contrib/workload-identity/upstream.env ]; then
		patch_tip=HEAD^
	fi
fi
patch_tip="$(git -C "$repo_root" rev-parse "$patch_tip^{commit}")"
if [ -n "${PATCH_SOURCE_REVISION_FILE:-}" ]; then
	printf '%s\n' "$patch_tip" > "$PATCH_SOURCE_REVISION_FILE"
fi

commits="$(git -C "$repo_root" rev-list --reverse "$base_tag..$patch_tip")"
upstream_changes="$(git -C "$repo_root" diff --name-status "$base_tag..$candidate_tag")"
boundary_changes=""
if [ -n "$upstream_changes" ]; then
	while IFS="$(printf '\t')" read -r status path rest; do
		[ -n "$path" ] || continue
		while IFS= read -r pattern; do
			case "$pattern" in ''|'#'*) continue ;; esac
			# shellcheck disable=SC2254 # trust-boundary entries are glob patterns
			case "$path" in $pattern) boundary_changes="${boundary_changes}${status}\t${path}\n"; break ;; esac
		done < "$repo_root/contrib/workload-identity/ci/trust-boundary.paths"
	done <<EOF
$upstream_changes
EOF
fi

equivalent_hits="$(git -C "$repo_root" grep -n -E 'ACTIONS_ID_TOKEN_REQUEST|WORKLOAD_IDENTITY|actionsOIDC|api/actions/oidc' "$candidate_tag" -- models modules routers services 2>/dev/null || true)"
risk=normal
if [ -n "$boundary_changes" ] || [ -n "$equivalent_hits" ]; then
	risk=high
fi

{
	printf '# Workload identity patch health\n\n'
	printf -- '- Previous upstream: `%s`\n' "$GITEA_PATCH_BASE_VERSION"
	printf -- '- Candidate upstream: `%s`\n' "$GITEA_CANDIDATE_VERSION"
	printf -- '- Patch tip: `%s`\n' "$patch_tip"
	printf -- '- Risk: **%s**\n' "$risk"
	printf -- '- Manual review required: **yes**\n\n'
	printf '## Patch replay\n\n'
} > "$report"

parent="$(dirname "$target")"
mkdir -p "$parent"
git -C "$repo_root" worktree add --detach "$target" "$candidate_tag" >/dev/null

applied=0
for commit in $commits; do
	subject="$(git -C "$repo_root" show -s --format=%s "$commit")"
	if git -C "$target" cherry-pick "$commit" >/dev/null 2>&1; then
		printf -- '- PASS `%s` %s\n' "$commit" "$subject" >> "$report"
		applied=$((applied + 1))
	else
		conflicts="$(git -C "$target" diff --name-only --diff-filter=U)"
		printf -- '- FAIL `%s` %s\n\n' "$commit" "$subject" >> "$report"
		printf '### Conflicts\n\n```text\n%s\n```\n' "${conflicts:-unknown conflict}" >> "$report"
		git -C "$target" cherry-pick --abort >/dev/null 2>&1 || true
		printf '\n## Result\n\nPatch replay failed after %s commits. Publication is forbidden.\n' "$applied" >> "$report"
		cat "$report"
		exit 1
	fi
done

{
	printf '\nAll %s patch commits applied cleanly.\n\n' "$applied"
	printf '## Relevant upstream changes\n\n'
	if [ -n "$boundary_changes" ]; then
		printf '```text\n%b```\n\n' "$boundary_changes"
	else
		printf 'None in the declared trust boundary.\n\n'
	fi
	printf '## Upstream workload identity signals\n\n'
	if [ -n "$equivalent_hits" ]; then
		printf 'Potential equivalent implementation detected; perform convergence review.\n\n```text\n%s\n```\n\n' "$equivalent_hits"
	else
		printf 'No equivalent implementation signal detected by the maintenance scan.\n\n'
	fi
	printf '## Validation gates\n\n'
} >> "$report"

printf '%s\n' "$target"
