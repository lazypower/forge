#!/bin/sh
set -eu

base=${1:?base revision is required}
head=${2:?head revision is required}
pull_ref=${DEPENDENCY_PR_REF:-}
output=${GITHUB_OUTPUT:-}
summary=${GITHUB_STEP_SUMMARY:-}

files=$(git diff --name-only --diff-filter=ACDMRTUXB "$base" "$head")
plan=review
verdict=review
reason='The update is outside the low-risk automerge policy.'

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
		if actions_are_pinned_updates; then
			plan=actions
			verdict=candidate
			reason='Patch-only GitHub Action updates change pinned uses lines and nothing else.'
		else
			reason='The action patch changes files or content beyond pinned uses lines.'
		fi
		;;
	dependabot/go_modules/automerge-go-patches-*)
		if files_are_go_graph; then
			plan=go
			verdict=candidate
			reason='Patch-only direct Go module updates changed only the module graph.'
		else
			reason='The Go patch changes files outside go.mod and go.sum.'
		fi
		;;
	dependabot/npm_and_yarn/automerge-frontend-patches-*)
		if files_are_frontend_graph; then
			plan=npm
			verdict=candidate
			reason='Patch-only direct frontend updates changed only the manifest and lockfile.'
		else
			reason='The frontend patch changes files outside package.json and pnpm-lock.yaml.'
		fi
		;;
esac

if [ -n "$output" ]; then
	{
		printf 'plan=%s\n' "$plan"
		printf 'verdict=%s\n' "$verdict"
		printf 'reason=%s\n' "$reason"
	} >>"$output"
else
	printf 'plan=%s\nverdict=%s\nreason=%s\n' "$plan" "$verdict" "$reason"
fi

if [ -n "$summary" ]; then
	{
		printf '## Dependency verdict\n\n'
		printf -- '- **Verdict:** `%s`\n' "$verdict"
		printf -- '- **Check plan:** `%s`\n' "$plan"
		printf -- '- **Reason:** %s\n' "$reason"
		printf '\nChanged files:\n\n```text\n%s\n```\n' "$files"
	} >>"$summary"
fi
