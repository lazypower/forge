#!/bin/sh
set -eu

base_report=${1:?base audit report is required}
head_report=${2:?head audit report is required}
summary=${GITHUB_STEP_SUMMARY:-}
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' 0

jq -e '.advisories | type == "object"' "$base_report" >/dev/null
jq -e '.advisories | type == "object"' "$head_report" >/dev/null

advisory_ids() {
	jq -r '.advisories[] | select(.severity == "high" or .severity == "critical") | (.github_advisory_id // (.id | tostring))' "$1" |
		LC_ALL=C sort -u
}

advisory_ids "$base_report" >"$work_dir/base"
advisory_ids "$head_report" >"$work_dir/head"
LC_ALL=C comm -13 "$work_dir/base" "$work_dir/head" >"$work_dir/new"

if [ ! -s "$work_dir/new" ]; then
	[ -z "$summary" ] || printf '\nNo new high or critical production advisories.\n' >>"$summary"
	exit 0
fi

printf '%s\n' 'new high or critical production advisories:' >&2
while IFS= read -r advisory; do
	jq -r --arg advisory "$advisory" \
		'.advisories[] | select((.github_advisory_id // (.id | tostring)) == $advisory) | "- \(.severity): \(.module_name): \(.title) (\(.url))"' \
		"$head_report" >&2
done <"$work_dir/new"

if [ -n "$summary" ]; then
	{
		printf '\n### New high or critical production advisories\n\n'
		while IFS= read -r advisory; do
			jq -r --arg advisory "$advisory" \
				'.advisories[] | select((.github_advisory_id // (.id | tostring)) == $advisory) | "- **\(.severity)** `\(.module_name)`: [\(.title)](\(.url))"' \
				"$head_report"
		done <"$work_dir/new"
	} >>"$summary"
fi

exit 1
