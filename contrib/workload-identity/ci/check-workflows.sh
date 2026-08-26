#!/bin/sh
set -eu

repo_root=$(cd -- "$(dirname "$0")/../../.." && pwd)
cd "$repo_root"

expected=$(printf '%s\n' \
	workload-identity-release.yml)
actual=$(find .github/workflows -maxdepth 1 -type f -exec basename {} \; | LC_ALL=C sort)

if [ "$actual" != "$expected" ]; then
	printf '%s\n' 'unexpected GitHub workflow set' >&2
	printf '%s\n' 'expected:' "$expected" 'actual:' "$actual" >&2
	exit 1
fi

printf '%s\n' 'repository workflow policy passed'
