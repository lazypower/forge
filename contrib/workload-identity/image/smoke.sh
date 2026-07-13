#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

set -eu

image_ref="${IMAGE_REF:?set IMAGE_REF to the image under test}"
target_platform="${TARGET_PLATFORM:-linux/amd64}"
container="gitea-workload-identity-smoke-$$"
port="${SMOKE_PORT:-3301}"
logs="$(mktemp)"

cleanup() {
	docker rm --force --volumes "$container" >/dev/null 2>&1 || true
	rm -f "$logs"
}
trap cleanup EXIT INT TERM

docker run --detach --name "$container" --publish "$port:3000" \
	-e GITEA_RUNNER_REGISTRATION_TOKEN=fixture-only-smoke-runner-token-000001 \
	-e GITEA__actions__ENABLED=true \
	-e GITEA__actions__WORKLOAD_IDENTITY_ENABLED=true \
	-e GITEA__database__DB_TYPE=sqlite3 \
	-e GITEA__database__PATH=/data/gitea/gitea.db \
	-e GITEA__oauth2__ENABLED=true \
	-e GITEA__oauth2__JWT_SIGNING_ALGORITHM=RS256 \
	-e GITEA__oauth2__JWT_SIGNING_PRIVATE_KEY_FILE=jwt/workload-identity.pem \
	-e GITEA__security__INSTALL_LOCK=true \
	-e "GITEA__server__ROOT_URL=http://localhost:$port/" \
	"$image_ref" >/dev/null

attempt=0
until curl --fail --silent "http://localhost:$port/api/healthz" >/dev/null 2>&1; do
	if ! docker inspect "$container" --format '{{.State.Running}}' | grep -qx true; then
		docker logs "$container" >&2
		exit 1
	fi
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 120 ]; then
		docker logs "$container" >&2
		exit 1
	fi
	sleep 1
done

docker logs "$container" >"$logs" 2>&1
grep -q 'ORM engine initialization successful' "$logs"

discovery="$(curl --fail --silent "http://localhost:$port/api/actions/oidc/.well-known/openid-configuration")"
test "$(printf '%s' "$discovery" | jq -er .issuer)" = "http://localhost:$port/api/actions/oidc"
curl --fail --silent "http://localhost:$port/api/actions/oidc/jwks" |
	jq -e '.keys | length == 1 and .[0].kty != "oct" and .[0].use == "sig"' >/dev/null

status="$(curl --silent --output /dev/null --write-out '%{http_code}' \
	"http://localhost:$port/api/actions/oidc/token?audience=vault")"
test "$status" = 401

curl --fail --silent -H 'Content-Type: application/json' --data '{}' \
	"http://localhost:$port/api/actions/ping.v1.PingService/Ping" >/dev/null

version="$(docker inspect "$image_ref" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')"
revision="$(docker inspect "$image_ref" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')"
platform="$(docker inspect "$image_ref" --format '{{.Os}}/{{.Architecture}}')"
elf_machine="$(docker exec "$container" sh -c "od -An -t x1 -j 18 -N 2 /app/gitea/gitea | tr -d ' \\n'")"
test "$version" = 1.26.4
test -n "$revision"
test "$platform" = "$target_platform"
case "$target_platform:$elf_machine" in
	linux/amd64:3e00|linux/arm64:b700) ;;
	*) printf 'unexpected Gitea ELF machine %s for %s\n' "$elf_machine" "$target_platform" >&2; exit 1 ;;
esac

printf 'image smoke test passed: %s (%s, %s)\n' "$image_ref" "$revision" "$platform"
