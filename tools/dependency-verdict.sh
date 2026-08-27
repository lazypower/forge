#!/bin/sh
set -eu

base=${1:?base revision is required}
head=${2:?head revision is required}
pull_ref=${DEPENDENCY_PR_REF:-}
pull_title=${DEPENDENCY_PR_TITLE:-}
output=${GITHUB_OUTPUT:-}
summary=${GITHUB_STEP_SUMMARY:-}

files=$(git diff --name-only --diff-filter=ACDMRTUXB "$base" "$head")
changed_files=$(printf '%s\n' "$files" | paste -sd ',' -)
plan='review'
verdict='review'
ecosystem='unknown'
update_class='unclassified'
rule='unrecognized-dependabot-update'
reason='Policy hold: the Dependabot branch does not match a configured dependency policy.'
risk='The repository cannot prove the ecosystem or update class from the bot-created branch.'
next_step='Inspect the dependency diff and upstream release or security notes, then choose focused validation for the affected runtime.'

files_are_go_graph() {
	for file in $files; do
		case "$file" in
			go.mod | go.sum) ;;
			*) return 1 ;;
		esac
	done
	[ -n "$files" ]
}

files_are_frontend_graph() {
	for file in $files; do
		case "$file" in
			package.json | pnpm-lock.yaml) ;;
			*) return 1 ;;
		esac
	done
	[ -n "$files" ]
}

files_are_action_definitions() {
	for file in $files; do
		case "$file" in
			.github/workflows/*.yml | .github/workflows/*.yaml | .github/actions/*/action.yml) ;;
			*) return 1 ;;
		esac
	done
	[ -n "$files" ]
}

actions_are_pinned_updates() {
	files_are_action_definitions || return 1
	changes=$(git diff --unified=0 --no-color "$base" "$head" |
		awk '/^[+-]/ && !/^---/ && !/^\+\+\+/ {print}')
	[ -n "$changes" ] || return 1
	printf '%s\n' "$changes" |
		grep -Ev '^[+-][[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+[^@[:space:]]+@[0-9a-f]{40}([[:space:]]+#.*)?$' |
		grep -q . && return 1
	return 0
}

case "$pull_ref" in
	dependabot/github_actions/automerge-actions-patches-*)
		ecosystem='GitHub Actions'
		update_class='patch'
		rule='automerge-actions-patches'
		if actions_are_pinned_updates; then
			plan='actions'
			verdict='candidate'
			reason='Candidate: a patch-only Actions group changed SHA-pinned uses lines and nothing else.'
			risk='Actions execute code on the runner; eligibility is limited to Dependabot-resolved patch revisions with unchanged workflow structure.'
			next_step='Run the workflow inventory check. A green result is eligible for automerge.'
		else
			reason='Policy hold: the patch candidate changed files or content beyond SHA-pinned uses lines.'
			risk='Unexpected workflow edits can change permissions, commands, inputs, or the code executed by the runner.'
			next_step='Review every non-pin workflow change and run make lint-actions; restore the PR to pin-only changes before treating it as an automerge candidate.'
		fi
		;;
	dependabot/github_actions/review-actions-minors-*)
		ecosystem='GitHub Actions'
		update_class='minor'
		rule='review-actions-minors'
		reason='Policy hold: GitHub Actions minor updates require human review.'
		risk='A minor Action release may change runner behavior, bundled runtimes, inputs, outputs, or permission expectations.'
		next_step='Review upstream release notes and the pinned revisions, then run make lint-actions and any workflow-specific validation justified by the changed Action.'
		;;
	dependabot/go_modules/automerge-go-patches-*)
		ecosystem='Go'
		update_class='patch'
		rule='automerge-go-patches'
		if files_are_go_graph; then
			plan='go'
			verdict='candidate'
			reason='Candidate: patch-only direct Go updates changed only go.mod and go.sum.'
			risk='Patch releases can still regress behavior; eligibility requires graph integrity and compilation across production and unit-test packages.'
			next_step='Verify and tidy-check the graph, then compile all backend packages. A green result is eligible for automerge.'
		else
			reason='Policy hold: the Go patch candidate changed files outside go.mod and go.sum.'
			risk='Source or configuration changes cannot be attributed solely to a module graph update.'
			next_step='Review the extra files and run focused tests for their behavior; remove unrelated changes before treating the PR as an automerge candidate.'
		fi
		;;
	dependabot/go_modules/review-go-minors-*)
		ecosystem='Go'
		update_class='minor'
		rule='review-go-minors'
		reason='Policy hold: direct Go module minor updates require human review.'
		risk='A minor release may add or alter APIs, defaults, generated behavior, transitive dependencies, or supported platforms.'
		next_step='Review module release notes and the graph diff, identify affected packages, and run focused tests for those packages.'
		;;
	dependabot/npm_and_yarn/automerge-frontend-patches-*)
		ecosystem='npm'
		update_class='patch'
		rule='automerge-frontend-patches'
		if files_are_frontend_graph; then
			plan='npm'
			verdict='candidate'
			reason='Candidate: patch-only direct npm updates changed only package.json and pnpm-lock.yaml.'
			risk='npm packages execute in the build and test toolchain; eligibility requires publisher-trust checks, no new serious advisories, and compatibility validation without lifecycle scripts.'
			next_step='Audit the advisory delta, install without lifecycle scripts, then lint, test, and build. A green result is eligible for automerge.'
		else
			reason='Policy hold: the npm patch candidate changed files outside package.json and pnpm-lock.yaml.'
			risk='Source, configuration, or generated-file edits cannot be attributed solely to the dependency graph.'
			next_step='Review the extra files and run focused validation for their behavior; remove unrelated changes before treating the PR as an automerge candidate.'
		fi
		;;
	dependabot/npm_and_yarn/review-frontend-minors-*)
		ecosystem='npm'
		update_class='minor'
		rule='review-frontend-minors'
		reason='Policy hold: direct npm minor updates require human review.'
		risk='A minor package release may change browser behavior, build output, plugin contracts, defaults, or the transitive supply chain.'
		next_step='Review package release notes and the lockfile delta, then select focused UI, build, or toolchain tests based on the affected packages.'
		;;
	dependabot/docker/*review-image-updates-*)
		ecosystem='container image'
		update_class='patch or minor'
		rule=review-image-updates
		reason='Policy hold: container base-image updates require human review.'
		risk='Even patch image updates can change the operating-system package set, entrypoint behavior, architecture support, or runtime compatibility.'
		next_step='Review the image digest and upstream changelog, build the workload-identity image, and exercise its startup and identity behavior.'
		;;
	dependabot/github_actions/*)
		ecosystem='GitHub Actions'
		update_class='ungrouped'
		rule='ungrouped-actions-update'
		reason='Policy hold: this Actions update was not generated by a configured patch or minor group.'
		risk='The branch does not prove patch-only scope; individual updates may be major, security-specific, or otherwise outside automatic eligibility.'
		next_step='Determine the version change from the PR, review upstream release or security notes, and run make lint-actions plus workflow-specific validation.'
		;;
	dependabot/go_modules/*)
		ecosystem='Go'
		update_class='ungrouped'
		rule='ungrouped-go-update'
		reason='Policy hold: this Go update was not generated by a configured patch or minor group.'
		risk='The branch does not prove patch-only scope; individual updates may be major, security-specific, or otherwise outside automatic eligibility.'
		next_step='Determine the version change from the PR, review module release or security notes, and run focused tests for affected packages.'
		;;
	dependabot/npm_and_yarn/*)
		ecosystem='npm'
		update_class='ungrouped'
		rule='ungrouped-npm-update'
		reason='Policy hold: this npm update was not generated by a configured patch or minor group.'
		risk='The branch does not prove patch-only scope; individual updates may be major, security-specific, or otherwise outside automatic eligibility.'
		next_step='Determine the version change from the PR, review package release or security notes, inspect the lockfile delta, and run focused frontend validation.'
		;;
	dependabot/docker/*)
		ecosystem='container image'
		update_class='ungrouped'
		rule='ungrouped-container-update'
		reason='Policy hold: this container update was not generated by the configured review group.'
		risk='The update is outside the repository image policy and may alter the base operating system or runtime contract.'
		next_step='Inspect the image reference and digest, review upstream changes, then build and exercise the affected image.'
		;;
esac

classification="$ecosystem $update_class update"

if [ -n "$output" ]; then
	{
		printf 'plan=%s\n' "$plan"
		printf 'verdict=%s\n' "$verdict"
		printf 'classification=%s\n' "$classification"
		printf 'rule=%s\n' "$rule"
		printf 'reason=%s\n' "$reason"
		printf 'risk=%s\n' "$risk"
		printf 'next_step=%s\n' "$next_step"
		printf 'changed_files=%s\n' "$changed_files"
	} >>"$output"
else
	printf 'plan=%s\nverdict=%s\nclassification=%s\nrule=%s\nreason=%s\nrisk=%s\nnext_step=%s\nchanged_files=%s\n' \
		"$plan" "$verdict" "$classification" "$rule" "$reason" "$risk" "$next_step" "$changed_files"
fi

if [ -n "$summary" ]; then
	{
		printf '## Dependency verdict\n\n'
		printf -- '- **Verdict:** `%s`\n' "$verdict"
		printf -- '- **Classification:** %s\n' "$classification"
		printf -- '- **Matched rule:** `%s`\n' "$rule"
		printf -- '- **Check plan:** `%s`\n' "$plan"
		[ -z "$pull_title" ] || printf -- '- **Pull request:** %s\n' "$pull_title"
		printf '\n### Decision\n\n%s\n' "$reason"
		printf '\n### Risk being controlled\n\n%s\n' "$risk"
		printf '\n### Required scrutiny\n\n%s\n' "$next_step"
		printf '\nChanged files:\n\n```text\n%s\n```\n' "$files"
	} >>"$summary"
fi
