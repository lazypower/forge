#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

repo_root="$(CDPATH='' cd -- "$(dirname "$0")/../../.." && pwd)"
. "$repo_root/contrib/workload-identity/upstream.env"
report="${FORK_HEALTH_REPORT:-$repo_root/fork-health.md}"
result_env="${RESULT_ENV:-$repo_root/verified-image.env}"
workspace="$(mktemp -d)"
source_revision="$(git -C "$repo_root" rev-parse HEAD)"

cleanup() {
	rm -rf "$workspace"
}
trap cleanup EXIT INT TERM

run_gate() {
	name="$1"
	shift
	if "$@"; then
		printf -- '- PASS %s\n' "$name" >> "$report"
	else
		printf -- '- FAIL %s\n\nRelease is forbidden.\n' "$name" >> "$report"
		cat "$report"
		exit 1
	fi
}

{
	printf '# Forge release health\n\n'
	printf -- '- Inherited Gitea lineage: `%s`\n' "$GITEA_PATCH_BASE_VERSION"
	printf -- '- Fork revision: `%s`\n\n' "$source_revision"
	printf '## Validation gates\n\n'
} > "$report"

cd "$repo_root"
run_gate 'format check' sh -c 'make fmt >/dev/null && git diff --exit-code'
run_gate 'generated bindata' env TAGS=bindata make generate-go
run_gate 'Go lint' make lint-go
run_gate 'Actions service tests' go test -tags 'sqlite sqlite_unlock_notify' ./services/actions
run_gate 'Gitea build' make gitea
run_gate 'runner integration test' env GITEA_TEST_DATABASE=sqlite make 'test-integration#TestActionsOIDCTokenIntegration'

build_output="$workspace/image-build.log"
if PATCH_REVISION="$source_revision" contrib/workload-identity/image/build.sh >"$build_output" 2>&1; then
	printf -- '- PASS image build\n' >> "$report"
else
	cat "$build_output"
	printf -- '- FAIL image build\n\nRelease is forbidden.\n' >> "$report"
	cat "$report"
	exit 1
fi
image_ref="$(sed -n 's/^IMAGE_REF=//p' "$build_output")"
image_digest="$(sed -n 's/^IMAGE_DIGEST=//p' "$build_output")"
fork_revision="$(sed -n 's/^PATCH_REVISION=//p' "$build_output")"
target_platform="$(sed -n 's/^TARGET_PLATFORM=//p' "$build_output")"
test -n "$image_ref" && test -n "$image_digest" && test -n "$fork_revision" && test -n "$target_platform"

rebuild_output="$workspace/image-rebuild.log"
if PATCH_REVISION="$source_revision" contrib/workload-identity/image/build.sh >"$rebuild_output" 2>&1 &&
	[ "$(sed -n 's/^IMAGE_REF=//p' "$rebuild_output")" = "$image_ref" ] &&
	[ "$(sed -n 's/^IMAGE_DIGEST=//p' "$rebuild_output")" = "$image_digest" ] &&
	[ "$(sed -n 's/^PATCH_REVISION=//p' "$rebuild_output")" = "$fork_revision" ] &&
	[ "$(sed -n 's/^TARGET_PLATFORM=//p' "$rebuild_output")" = "$target_platform" ]; then
	printf -- '- PASS reproducible image rebuild\n' >> "$report"
else
	cat "$rebuild_output"
	printf -- '- FAIL reproducible image rebuild\n\nRelease is forbidden.\n' >> "$report"
	cat "$report"
	exit 1
fi

run_gate 'image smoke test' env IMAGE_REF="$image_ref" UPSTREAM_VERSION="$GITEA_PATCH_BASE_VERSION" contrib/workload-identity/image/smoke.sh
run_gate 'Vault acceptance' env GITEA_IMAGE="$image_ref" SKIP_ACCEPTANCE_BUILD=1 contrib/workload-identity/acceptance/run.sh

{
	printf '\n## Image\n\n'
	printf -- '- Reference: `%s`\n' "$image_ref"
	printf -- '- Digest: `%s`\n' "$image_digest"
	printf -- '- Fork revision: `%s`\n' "$fork_revision"
	printf -- '- Platform: `%s`\n' "$target_platform"
	printf '\n## Result\n\nAll gates passed. Human approval is still required before publication or deployment.\n'
} >> "$report"

{
	printf 'IMAGE_REF=%s\n' "$image_ref"
	printf 'IMAGE_DIGEST=%s\n' "$image_digest"
	printf 'PATCH_REVISION=%s\n' "$fork_revision"
	printf 'TARGET_PLATFORM=%s\n' "$target_platform"
} > "$result_env"

cat "$report"
