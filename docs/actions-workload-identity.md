# Gitea Actions workload identity

This private patch lets an explicitly authorized Gitea Actions job exchange its
live task identity for a short-lived JWT suitable for Vault's JWT auth method.
It is based on Gitea `v1.27.2` and is disabled by default.

## Security contract

A workflow must declare:

```yaml
permissions:
  id-token: write
```

The server injects the GitHub-compatible request context only after applying
the job permission and repository/owner ceilings. Gitea Runner 1.0 may still
construct the standard request variables from its general task context when
the permission-specific fields are absent; this does not confer authority.
The issuance endpoint independently authenticates the exact task credential,
checks the permission again, and denies the request.

The caller supplies exactly one `audience` query value. It cannot supply or
override repository, owner, ref, SHA, workflow, job, run, actor, event, subject,
or arbitrary claims. Unknown or repeated parameters are rejected.

Issuance is permitted only while all of the following remain true:

- the feature is enabled;
- the task, its job, and its run are running;
- the task is the job's latest task and current attempt;
- task, job, and repository identifiers agree;
- effective `id-token` permission is `write`; and
- the configured signer is asymmetric.

Completion, cancellation, supersession, an old attempt, or permission removal
prevents new issuance. A JWT already issued remains usable until its five-minute
expiry.

## Configuration

Set a canonical, externally stable `ROOT_URL`. Issuer and endpoint URLs are
derived only from this value, never request or forwarding headers.

```ini
[server]
ROOT_URL = https://git.example.net/forge/

[actions]
ENABLED = true
WORKLOAD_IDENTITY_ENABLED = true

[oauth2]
ENABLED = true
JWT_SIGNING_ALGORITHM = RS256
JWT_SIGNING_PRIVATE_KEY_FILE = jwt/private.pem
```

`ES256`, other supported RSA/ECDSA algorithms, and `EdDSA` are also asymmetric.
HMAC algorithms (`HS256`, `HS384`, and `HS512`) are rejected when workload
identity is enabled because their verification secret cannot be published as
JWKS. Startup fails rather than silently exposing a broken issuer.

Enabling workload identity currently reuses Gitea's OAuth2 signing key. There
is one active key and no overlap set. Replacing it immediately invalidates all
outstanding workload and applicable OAuth2 tokens. Since workload JWTs expire
within five minutes, schedule rotation after that drain window or accept the
brief authentication interruption. Back up the key file separately from the
container image and restrict it like any other signing key.

Endpoints for the example above are:

- issuer: `https://git.example.net/forge/api/actions/oidc`
- discovery: `https://git.example.net/forge/api/actions/oidc/.well-known/openid-configuration`
- JWKS: `https://git.example.net/forge/api/actions/oidc/jwks`
- token request: `https://git.example.net/forge/api/actions/oidc/token`

All endpoints return 404 while the feature is disabled.

## Token contract

Registered claims are `iss`, `sub`, `aud`, `iat`, `nbf`, `exp`, and `jti`.
Identity claims are:

| Claim | Authority |
| --- | --- |
| `repository`, `repository_id` | run repository |
| `repository_owner`, `repository_owner_id` | run repository owner |
| `ref`, `ref_type`, `sha` | triggering ref and commit; pull refs have type `branch`, while `pull_request_target` uses the base branch |
| `workflow` | workflow-source-repository-relative path |
| `workflow_ref` | `<owner>/<repo>/<workflow>@<workflow_sha>` |
| `workflow_sha` | commit from which the workflow was loaded |
| `workflow_repository`, `workflow_repository_id` | repository containing the top-level workflow |
| `job` | workflow job key |
| `event_name` | run trigger event |
| `actor`, `actor_id` | triggering user |
| `run_id`, `run_number`, `run_attempt` | current run and task attempt |

The fixed subject is:

```text
repo:<owner_id>/<repository_id>:ref:<path-escaped-ref>
```

Names aid diagnostics. Vault policy should bind immutable numeric IDs and then
add ref or workflow restrictions appropriate to the secret.

Gitea 1.27.2 records the top-level workflow's source repository and immutable
commit separately from the repository whose event started the run. The patch
uses that source authority for repository and scoped workflows, and exposes its
name and numeric ID explicitly. Workload identity currently fails closed for
jobs inside reusable workflows because their caller ancestry is not yet part of
the token contract. Supporting those jobs requires explicit ancestry claims,
negative tests, and Vault policy review.

## Vault configuration

Enable Vault's JWT auth method and configure the exact path-bearing issuer:

```sh
vault auth enable jwt
vault write auth/jwt/config \
  oidc_discovery_url=https://git.example.net/forge/api/actions/oidc \
  bound_issuer=https://git.example.net/forge/api/actions/oidc
```

Use JSON for roles so numeric claims remain numbers. Replace the example IDs:

```json
{
  "role_type": "jwt",
  "user_claim": "sub",
  "bound_audiences": ["vault"],
  "bound_claims": {
    "repository_owner_id": 42,
    "repository_id": 314,
    "ref": "refs/heads/main",
    "workflow": ".gitea/workflows/deploy.yaml"
  },
  "token_policies": ["deploy"],
  "token_ttl": 60,
  "token_max_ttl": 120,
  "token_explicit_max_ttl": 120
}
```

A job can request and exchange a token without storing a Vault credential:

```sh
jwt="$(curl --fail --silent \
  -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
  "$ACTIONS_ID_TOKEN_REQUEST_URL?audience=vault" | jq -r .value)"

curl --fail --silent \
  --data "$(jq -cn --arg jwt "$jwt" '{role:"deploy",jwt:$jwt}')" \
  https://vault.example.net/v1/auth/jwt/login
```

The disposable acceptance fixture in
`contrib/workload-identity/acceptance` contains a complete configuration with
no real credentials.

## Threat model

Gitea's database state is the identity authority. The task credential proves
possession of one task; the runner and job container transport it but do not
choose claims. Vault is the authorization authority and must narrowly bind
roles and policies.

The patch protects against caller claim substitution, stale or cross-task
credentials, missing permission, symmetric-key publication, audience
confusion, modified JWTs, and host-header issuer changes.

It does not make untrusted execution safe. Code running in a permitted job, a
compromised action, or a compromised runner can request and exfiltrate the JWT
and every Vault credential legitimately granted to that identity. It can use
those credentials until they expire. Isolate runners by trust level, pin or
review actions, restrict fork approvals, bind Vault to immutable IDs plus a
trusted ref/workflow, and keep Vault leases short.

Other residual risks are signing-key compromise, a compromised Gitea database
or process, clock drift beyond the 30-second `nbf` allowance, and policy drift
in Vault. Monitor failed JWT logins and unexpected issuer/JWKS changes without
logging request credentials or issued JWTs.

## Verification

Run focused tests and the real acceptance environment:

```sh
go test -tags 'sqlite sqlite_unlock_notify' ./services/actions
make gitea
make 'test-integration#TestActionsOIDCTokenIntegration'
contrib/workload-identity/acceptance/run.sh
```

The acceptance fixture proves valid Vault exchange, short Vault leases,
permission denial, repository policy isolation, signature and audience
rejection, completed-task replay denial, parameter injection rejection, and
canonical discovery/JWKS behavior behind a subpath reverse proxy.
