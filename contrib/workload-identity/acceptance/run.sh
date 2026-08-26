#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

repo_root="$(CDPATH='' cd -- "$(dirname "$0")/../../.." && pwd)"
cd "$repo_root"
. contrib/workload-identity/lineage.env
export GITEA_UPSTREAM_VERSION="$GITEA_LINEAGE_VERSION"
export GITEA_UPSTREAM_COMMIT="$GITEA_LINEAGE_COMMIT"
export GITEA_PATCH_REVISION="${PATCH_REVISION:-$(git rev-parse HEAD)}"
export SOURCE_DATE_EPOCH="$(git show -s --format=%ct "$GITEA_PATCH_REVISION")"
cd contrib/workload-identity/acceptance

compose() {
	docker compose -f compose.yaml "$@"
}

api() {
	curl --fail --silent --show-error --user fixture-admin:fixture-only-admin-password \
		"http://localhost:3300/forge/api/v1$1"
}

api_json() {
	method="$1"
	path="$2"
	body="$3"
	curl --fail --silent --show-error --user fixture-admin:fixture-only-admin-password \
		-H 'Content-Type: application/json' -X "$method" --data "$body" \
		"http://localhost:3300/forge/api/v1$path"
}

vault() {
	method="$1"
	path="$2"
	body="${3:-}"
	curl --fail --silent --show-error -H 'X-Vault-Token: fixture-only-vault-root' \
		-H 'Content-Type: application/json' -X "$method" --data "$body" \
		"http://localhost:38200/v1$path"
}

create_repo() {
	name="$1"
	api_json POST /user/repos "{\"name\":\"$name\",\"auto_init\":true,\"default_branch\":\"main\"}"
}

install_workflow() {
	repo="$1"
	file="$2"
	content="$(base64 < "workflows/$file" | tr -d '\n')"
	api_json POST "/repos/fixture-admin/$repo/contents/.gitea/workflows/$file" \
		"{\"branch\":\"main\",\"message\":\"test: install workload identity acceptance workflow\",\"content\":\"$content\"}" >/dev/null
}

wait_for_run() {
	repo="$1"
	attempt=0
	while [ "$attempt" -lt 120 ]; do
		response="$(api "/repos/fixture-admin/$repo/actions/runs?limit=1")"
		status="$(printf '%s' "$response" | jq -r '.workflow_runs[0].status // "missing"')"
		conclusion="$(printf '%s' "$response" | jq -r '.workflow_runs[0].conclusion // ""')"
		if [ "$status" = completed ]; then
			test "$conclusion" = success || {
				printf '%s\n' "workflow $repo failed with conclusion $conclusion" >&2
				return 1
			}
			return 0
		fi
		attempt=$((attempt + 1))
		sleep 1
	done
	printf '%s\n' "workflow $repo did not complete" >&2
	return 1
}

cleanup() {
	if [ "${KEEP_ACCEPTANCE_ENVIRONMENT:-0}" = 1 ]; then
		return
	fi
	compose down --volumes --remove-orphans
}

trap cleanup EXIT INT TERM
cleanup
if [ "${SKIP_ACCEPTANCE_BUILD:-0}" = 1 ]; then
	compose up --no-build --detach gitea proxy vault probe
else
	compose up --build --detach gitea proxy vault probe
fi

attempt=0
until curl --fail --silent http://localhost:3300/forge/api/healthz >/dev/null 2>&1; do
	attempt=$((attempt + 1))
	test "$attempt" -lt 120
	sleep 1
done

compose exec --user git gitea gitea admin user create \
	--username fixture-admin \
	--password fixture-only-admin-password \
	--email fixture@example.invalid \
	--admin \
	--must-change-password=false

authorized_repo="$(create_repo workload-authorized)"
authorized_id="$(printf '%s' "$authorized_repo" | jq -er .id)"
create_repo workload-unauthorized >/dev/null
create_repo workload-other >/dev/null

vault POST /sys/auth/jwt '{"type":"jwt"}' >/dev/null
vault POST /auth/jwt/config '{"oidc_discovery_url":"http://proxy:8080/forge/api/actions/oidc","bound_issuer":"http://proxy:8080/forge/api/actions/oidc"}' >/dev/null
vault PUT /sys/policies/acl/workload '{"policy":"path \"secret/data/acceptance\" { capabilities = [\"read\"] }"}' >/dev/null
vault POST /secret/data/acceptance '{"data":{"result":"accepted"}}' >/dev/null
role="$(jq -cn --argjson repository_id "$authorized_id" '{role_type:"jwt",user_claim:"sub",bound_audiences:["vault"],bound_claims:{repository_id:$repository_id},token_policies:["workload"],token_ttl:60,token_max_ttl:120,token_explicit_max_ttl:120}')"
vault POST /auth/jwt/role/authorized "$role" >/dev/null

compose up --detach runner
sleep 3
install_workflow workload-authorized authorized.yaml
install_workflow workload-unauthorized unauthorized.yaml
install_workflow workload-other other-repository.yaml

wait_for_run workload-authorized
wait_for_run workload-unauthorized
wait_for_run workload-other

replay_status="$(curl --fail --silent http://localhost:38080/replay | jq -er .status)"
test "$replay_status" = 401 || test "$replay_status" = 403

discovery="$(curl --fail --silent http://localhost:3300/forge/api/actions/oidc/.well-known/openid-configuration)"
test "$(printf '%s' "$discovery" | jq -er .issuer)" = http://proxy:8080/forge/api/actions/oidc
curl --fail --silent "$(printf '%s' "$discovery" | jq -er .jwks_uri | sed 's|http://proxy:8080|http://localhost:3300|')" | jq -e '.keys | length == 1 and .[0].kty != "oct"' >/dev/null

printf '%s\n' 'workload identity acceptance passed'
