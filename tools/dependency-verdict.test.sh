#!/bin/sh
set -eu

repo_root=$(cd -- "$(dirname "$0")/.." && pwd)
verdict_script=$repo_root/tools/dependency-verdict.sh
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' 0

git -C "$fixture" init -q -b main
git -C "$fixture" config user.email dependency-gate@example.invalid
git -C "$fixture" config user.name dependency-gate

printf '%s\n' '{"dependencies":{"example":"1.0.0"}}' >"$fixture/package.json"
printf '%s\n' 'lockfileVersion: 9' 'example: 1.0.0' >"$fixture/pnpm-lock.yaml"
git -C "$fixture" add package.json pnpm-lock.yaml
git -C "$fixture" commit -q -m base

git -C "$fixture" switch -q -c dependency
printf '%s\n' '{"dependencies":{"example":"1.0.1"}}' >"$fixture/package.json"
printf '%s\n' 'lockfileVersion: 9' 'example: 1.0.1' >"$fixture/pnpm-lock.yaml"
git -C "$fixture" commit -qam dependency
dependency_head=$(git -C "$fixture" rev-parse HEAD)

git -C "$fixture" switch -q main
printf '%s\n' 'base advanced after Dependabot opened its pull request' >"$fixture/policy.md"
git -C "$fixture" add policy.md
git -C "$fixture" commit -q -m policy
base=$(git -C "$fixture" rev-parse HEAD)
git -C "$fixture" merge -q --no-ff --no-edit dependency
proposed=$(git -C "$fixture" rev-parse HEAD)
output=$fixture/output

classify() {
	(
		cd "$fixture"
		DEPENDENCY_PR_REF=$1 \
			DEPENDENCY_PR_TITLE='build(deps): synthetic policy test' \
			GITHUB_OUTPUT=$output \
			"$verdict_script" "$base" "$2"
	)
}

assert_output() {
	grep -Fqx "$1=$2" "$output" || {
		printf 'expected %s=%s in classifier output:\n' "$1" "$2" >&2
		cat "$output" >&2
		exit 1
	}
}

assert_review_case() {
	: >"$output"
	classify "$1" "$proposed"
	assert_output verdict review
	assert_output classification "$2"
	assert_output rule "$3"
	grep -Eq '^reason=Policy hold: .+' "$output"
	grep -Eq '^risk=.+' "$output"
	grep -Eq '^next_step=.+' "$output"
}

: >"$output"
classify dependabot/npm_and_yarn/automerge-frontend-patches-fixture "$proposed"
assert_output verdict candidate
assert_output classification 'npm patch update'
assert_output rule automerge-frontend-patches
assert_output changed_files package.json,pnpm-lock.yaml
grep -Eq '^risk=.+' "$output"
grep -Eq '^next_step=.+' "$output"

assert_review_case \
	dependabot/npm_and_yarn/review-frontend-minors-fixture \
	'npm minor update' \
	review-frontend-minors
grep -Fqx 'reason=Policy hold: direct npm minor updates require human review.' "$output"
grep -Fqx 'next_step=Review package release notes and the lockfile delta, then select focused UI, build, or toolchain tests based on the affected packages.' "$output"

assert_review_case \
	dependabot/github_actions/review-actions-minors-fixture \
	'GitHub Actions minor update' \
	review-actions-minors
assert_review_case \
	dependabot/go_modules/review-go-minors-fixture \
	'Go minor update' \
	review-go-minors
assert_review_case \
	dependabot/docker/contrib/workload-identity/image/review-image-updates-fixture \
	'container image patch or minor update' \
	review-image-updates
assert_review_case \
	dependabot/npm_and_yarn/example-2.0.0 \
	'npm ungrouped update' \
	ungrouped-npm-update

: >"$output"
classify dependabot/npm_and_yarn/automerge-frontend-patches-fixture "$dependency_head"
assert_output verdict review
assert_output changed_files package.json,pnpm-lock.yaml,policy.md

printf '%s\n' 'dependency verdict policy tests passed'
