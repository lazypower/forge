# Model Context Protocol

Forge has an experimental Model Context Protocol (MCP) endpoint over stateless
Streamable HTTP. The endpoint, client bootstrap, Work inspection, and Work
mutation are separate capability layers, all disabled by default. Enabling the
endpoint alone preserves the ADR 0002 read-only `pull_request.inspect` surface.

## Enablement and endpoint

Configure an externally correct HTTPS `ROOT_URL`, including any installation
subpath, configure Forge's matching OAuth issuer, and enable the endpoint:

```ini
[server]
ROOT_URL = https://forge.example/forge/

[oauth2]
ENABLED = true
JWT_CLAIM_ISSUER = https://forge.example/forge

[mcp]
ENABLED = true
```

The endpoint is the configured subpath followed by `/mcp`; the example above is
`https://forge.example/forge/mcp`. Forge refuses to enable MCP when `ROOT_URL`
is not HTTPS.

### Default-off capability layers

| Configuration | Advertised tools |
| --- | --- |
| `ENABLED = false` | No MCP endpoint |
| `ENABLED = true`, both Work flags false | `pull_request.inspect` only |
| `WORK_INSPECTION_ENABLED = true` | Adds `work_item.inspect` and `work_plan.inspect` |
| `WORK_MUTATION_ENABLED = true`, OAuth authentication | Adds `work_plan.begin`, `work_item.revise`, and `work_plan.revise` |

Work inspection accepts an enabled OAuth MCP profile or the explicitly
selected read-only PAT fallback. Work mutation requires the exact OAuth
Work Planning profile. A PAT never advertises or authorizes mutation tools even if the
mutation flag is true. Enabling either Work flag does not opt a Project into
planning; the Project remains an ordinary board until an authorized user
explicitly begins a draft plan.

## Authentication profiles

`AUTHENTICATION` selects exactly one MCP credential profile. It defaults to
`oauth`. Forge never accepts PAT and OAuth credentials concurrently at the MCP
endpoint. Enabling MCP with the default profile fails closed unless the OAuth
and issuer requirements below are satisfied.

### Temporary PAT fallback

For a temporary rollback during the transition to OAuth, select PAT explicitly:

```ini
[mcp]
ENABLED = true
AUTHENTICATION = pat
```

Create a personal access token whose only scope is `read:repository`. Store the
token in the MCP client's secret storage separately from the server URL, and
send it only as an `Authorization: Bearer …` header. Tokens in URLs, forms,
cookies, Basic authentication, OAuth tokens, and Actions credentials are not
accepted. Do not embed the token in configuration that may be logged or shared.

This fallback is temporary, is expected to remain only for a few release
cycles, and is not MCP OAuth conformance. Forge PATs do not expire and are not
audience-bound, so the same PAT may be accepted by other Forge API endpoints.
The OAuth profile described below is not active while `AUTHENTICATION = pat`.

### OAuth profile

OAuth is the primary and default profile:

```ini
[server]
ROOT_URL = https://forge.example/forge/

[oauth2]
ENABLED = true
JWT_CLAIM_ISSUER = https://forge.example/forge

[mcp]
ENABLED = true
```

`JWT_CLAIM_ISSUER` is required and, ignoring one trailing slash, must equal
`ROOT_URL`. Forge rejects external issuer aliases because its own
`/.well-known/openid-configuration` and
`/.well-known/oauth-authorization-server` endpoints are the
authorization-server discovery authority. Both values must use HTTPS.

MCP client bootstrap is independently disabled by default. Enable it only with
the OAuth MCP profile:

```ini
[mcp]
ENABLED = true
AUTHENTICATION = oauth
CLIENT_BOOTSTRAP_ENABLED = true
```

When enabled, Forge advertises `/login/oauth/register` as its OAuth client
registration endpoint. Each harness installation can silently obtain a
high-entropy public client ID. Forge issues no client secret, accepts no scope,
profile, repository, principal, confidential-client method, software
statement, fetched URL, or other extension metadata at bootstrap, and creates
no grant, code, token, or repository authority before consent.

The closed registration profile accepts a bounded client name, an optional
bounded installation label, `authorization_code` plus `refresh_token`, response
type `code`, token authentication method `none`, and at most the configured
number of redirects. Redirects must all belong to one class: exact HTTPS URLs,
or native `http` URLs whose host is a literal IPv4 or IPv6 loopback address.
User information, fragments, non-loopback HTTP, mixed redirect classes,
duplicates, and malformed URLs are rejected without fetching them. HTTPS
redirects match byte-for-byte. A loopback authorization may select a runtime
port while every other component remains exact.

A new registration remains provisional and authority-free for 30 minutes by
default. The configurable lifetime must remain between 10 and 60 minutes. The
first successful login and consent atomically finalizes the registration and
binds it to that principal. Another principal cannot reuse it. Denial or expiry
deletes the provisional registration, and expiry during login or consent
creates no grant. Finalized names, installation labels, redirect classes, and
redirects are immutable. An ungranted registration can be deleted only by its
bound principal from Applications settings.

Bootstrap has separate request-body, in-flight, per-source, instance-rate,
source-bucket, redirect-count, outstanding-registration, expiry, and bounded
cleanup limits. The per-source bucket uses the direct connection address;
non-IP peers share one bucket.
Source rotation can temporarily fill the instance-wide provisional cap and
deny new onboarding; that bounded storage availability tradeoff does not affect
approved clients. Turning bootstrap off stops new registrations and removes
the discovery advertisement without invalidating existing grants.

The earlier pre-release shared read and work-planning clients are removed.
They have no compatibility boundary: no aliases, backfills, fixed-client
transitions, or credential-lineage migration are provided. Clean-slate
evaluation must use a fresh database and fresh credentials.

Authorization requires the exact MCP resource URL, one of the exact scope
profiles described below, and PKCE `S256`. The resource is the configured
`ROOT_URL`, including its subpath, followed by `mcp`; the example resource is
`https://forge.example/forge/mcp`. Access and refresh tokens are signed and
bound to that exact audience. Refresh-token use rotates the grant counter even
when global legacy rotation is disabled. Each finalized installation has its
own independently revocable client and refresh lineage.

OAuth Protected Resource Metadata is served by the official MCP Go SDK at the
application-scoped `/.well-known/oauth-protected-resource/mcp` route and is
advertised explicitly in bearer challenges. Forge's automated interoperability
coverage drives the official MCP Go SDK client from that challenge through
protected-resource and Forge OpenID Connect discovery, dynamic client
bootstrap, authorization with PKCE `S256`, loopback callback, token exchange,
an authenticated `pull_request.inspect` call, access-token refresh, refresh
rotation, and replay rejection. It also covers closed metadata, redirect,
scope, audience, credential-profile, unrelated-resource, configured-subpath,
and TLS trust boundaries. This does not claim Client ID Metadata Documents,
external issuer aliases, or broader MCP or OAuth conformance.

### Work mutation OAuth profile

`Read` requires exactly `read:repository`. When
`WORK_MUTATION_ENABLED = true`, a bootstrapped installation may
request the `Work Planning` profile with the exact canonical scope set
`read:repository write:issue write:repository`, the canonical MCP audience,
PKCE `S256`, and explicit consent. A registration itself does not select or own
a profile. Scope order in the authorization request is immaterial, but missing,
duplicate, unknown, or additional scopes fail closed.

Changing an existing registration from Read to Work Planning, or back, opens
new browser consent. Approval atomically replaces its grant and invalidates
every old authorization code, access token, and refresh token. Old credentials
cannot inherit the new profile or restore the previous one. Denial or failed
replacement leaves the previous grant and credentials unchanged. Do not revoke
first just to change profiles; the approved replacement is the boundary.

The consent explains that Work planning can create, edit, close, and reopen
Issues; change plan membership and dependencies; and create, activate, return
to draft, or delete repository plans wherever current native permissions allow.
It cannot push or merge code, administer repositories, or run agents.

Read OAuth and PAT credentials cannot invoke mutation tools. Work Planning
OAuth tokens cannot authorize REST, and general Forge OAuth tokens cannot
authorize MCP. Every invocation rechecks repository state, Issues and Projects
units, dependency configuration, and the principal's current native
permissions. Disabling Work mutation makes the Work Planning profile
unissuable and removes the mutation tools; a retained registration or grant is
not enabled authority.

## Client onboarding and reconnect

Point a conforming client at `https://forge.example/forge/mcp` and use automatic
OAuth discovery and registration. The normal user flow is connect, browser
login when needed, review the Read or Work Planning consent, and approve or
deny. Users do not create an application in settings or copy a client ID.
Treat all registered names as client-provided and unverified: check the
requested profile, exact scopes, and loopback class or HTTPS callback origin,
not just a familiar-looking client label.

Client implementers can reproduce the bootstrap request below. The response
contains public registration metadata and a generated `client_id`, never a
client secret. Use the discovered `registration_endpoint`; this URL illustrates
the configured subpath. A harness should perform this step itself and persist
the response for that installation.

```sh
umask 077
cat > registration.json <<'JSON'
{
  "client_name": "Example Harness",
  "installation_name": "Example laptop",
  "redirect_uris": ["http://127.0.0.1/callback"],
  "application_type": "native",
  "token_endpoint_auth_method": "none",
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"]
}
JSON
curl --fail-with-body --silent --show-error \
  --header 'Content-Type: application/json' \
  --data-binary @registration.json \
  --output installation-registration.json \
  https://forge.example/forge/login/oauth/register
```

Use `application_type: "web"` and, for example,
`https://client.example/oauth/callback` for an HTTPS client. Do not mix HTTPS and
loopback redirects in one registration. Client and installation names are at
most 128 characters, with no surrounding whitespace or control/format
characters; redirects are at most 2,048 bytes each. Omit an unused optional
installation name. No `scope`, `resource`, principal, or repository field is
accepted in the registration body: the MCP audience is server-defined.

After registration, the client performs the authorization-code flow:

1. Listen on the registered callback path (a native client may choose a free
   loopback port). Generate a fresh random `state` and PKCE verifier; send its
   S256 challenge, `response_type=code`, the saved `client_id`, exact
   `redirect_uri`, `resource=https://forge.example/forge/mcp`, and the desired
   exact scope set to the discovered authorization endpoint.
2. Open that request in the browser. Validate the callback `state`; exchange
   its one-use code at the discovered token endpoint using the same client ID,
   redirect URI, resource, and PKCE verifier. Do not send a client secret.
3. Store access and refresh credentials in the installation's secret storage.
   Send the access token only in the bearer header. Refresh at the token
   endpoint with `grant_type=refresh_token`, the client ID, refresh token, and
   exact resource; atomically save the new pair. Serialize refreshes within an
   installation so two local callers do not race the rotating refresh token.

Normal credential reuse and successful refresh require no additional browser
gate. Do not bootstrap or request consent before every operation. Two
installations, even with identical client and installation labels, must each
bootstrap once and keep separate client IDs and credential stores. Labels do
not deduplicate registrations or share grants.

| Event | Client and user action |
| --- | --- |
| Access expires, refresh is valid | Refresh silently and replace the stored pair. |
| Local logout or loss of access/refresh credentials | Keep the saved client ID if available; reconnect through browser authorization. This does not itself revoke the server grant. |
| Client ID is also lost, or the provisional registration expired | Bootstrap again and obtain consent; revoke any abandoned grant in Applications settings. |
| Server grant revoked | Stop using its credentials. The inert registration remains; reconnect with it through new browser consent. |
| Profile changes | Ask for the other exact profile and obtain new consent; discard all credentials from the replaced grant after success. |
| Registered name, installation label, or callback must change | Bootstrap a new registration, consent, then revoke the old grant and delete the inert old registration. There is no registration-edit endpoint. |

Applications settings answer **what authority is currently delegated**:
registered client/installation metadata, server-defined profile, exact scopes,
authorization time, last credential issuance/rotation time, current enabled
state, public client/PKCE and redirect class, and revoke. Rotation time is not
last use. Ungranted finalized registrations appear separately and remain inert
until reauthorized or deleted by the bound principal. Deletion is unavailable
while a grant exists and invalidates remaining codes; it does not rewrite
historical receipt labels. Operation history instead answers **what happened**
using principal authority, registered metadata, and explicitly client-reported
harness/model snapshots. Models do not belong on the grant.

## Work planning

### Explicit Project opt-in

Database migration v344 adds a Project planning state whose default is
`disabled`. Existing and newly created ordinary Projects therefore retain their
board behavior after upgrade. An authorized `work_plan.begin` call either
creates a new repository Project in `draft` or opts one selected disabled
repository Project into `draft`. Activation is a separate guarded transition
using a just-in-time plan token. Project columns, labels, assignments, and task
checkboxes do not determine readiness.

Work projections compose current native Project, Issue, Project-Issue,
dependency, pull request, revision, and commit-status facts. Forge does not
persist a Work projection or copied readiness. Closed or archived repositories,
disabled required units, hidden prerequisites, invalid dependency graphs, and
stale content versions or plan tokens fail closed as appropriate. New
dependency creation is limited to Issues in the same repository.

### Tools

- `work_item.inspect` composes one Issue-centered item and an optional selected
  plan context.
- `work_plan.inspect` composes one bounded Project plan page and ready frontier.
- `work_plan.begin` creates a draft plan or opts a disabled Project into draft.
- `work_item.revise` conditionally changes one Issue title, Markdown, or state.
- `work_plan.revise` atomically applies a bounded closed set of membership,
  Issue creation, dependency, and plan-lifecycle changes.

The tools expose semantic Work operations, not generic Project or Issue CRUD,
consumer-defined queries, or a generic batch language. Read pages are
permission-rechecked, signed, deterministic, non-snapshot views and must be
reinspected before action.

### Receipts, replay, and provenance

Every Work mutation requires a harness name from the official MCP SDK's
`ClientInfo`: per-request `io.modelcontextprotocol/clientInfo` metadata, or the
initialized session's client information when absent. The model is required in
the closed request `_meta` entry `io.gitea.forge/clientAttribution`:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "work_plan.begin",
    "arguments": {
      "repository": {"owner": "example-owner", "name": "example-repo"},
      "idempotencyKey": "replace-with-a-fresh-random-key",
      "begin": {"kind": "new", "title": "Example plan"}
    },
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientCapabilities": {},
      "io.modelcontextprotocol/clientInfo": {"name": "Example Harness", "version": "1.0"},
      "io.gitea.forge/clientAttribution": {"model": "Example Model"}
    }
  }
}
```

This illustrates a stateless `2026-07-28` request. Send it to the MCP resource
with `Content-Type: application/json`, `Accept: application/json,
text/event-stream`, `MCP-Protocol-Version: 2026-07-28`, `MCP-Method: tools/call`,
and the OAuth bearer header. Use a fresh high-entropy idempotency key for a new
operation (for example, a randomly generated UUID), then retain the exact input
and key for retries. The metadata belongs in `params._meta`, outside
`arguments`; never put a model label into the Work semantic input. Older
negotiated sessions use their initialized client information when no
per-request override is present.

Forge trims the decoded labels and requires nonempty harness and model strings
of at most 128 UTF-8 characters each, with no control characters. A supplied
version must be nonempty and at most 64 characters under the same rules; an
absent version is omitted from output. The legacy SDK cannot distinguish an
omitted, empty, or null initialized version, so its empty value is treated as
absent. The Forge attribution object permits only the `model` field. Invalid
attribution fails with `client_attribution_required`, non-retryable until
corrected, before receipt lookup or Work access. Structurally malformed standard
MCP messages rejected by the SDK remain protocol errors.

Migration v345 adds narrow MCP Work receipts and stable links to affected native
Projects, Issues, and timeline events. Migration v348 extends the empty receipt
schema with immutable registered client/installation labels and client-reported
harness, optional version, model, and source snapshots. A receipt preserves
operation identity, domain-separated keyed request and idempotency digests, the
verified principal and OAuth authority snapshot (including profile and scope),
MCP origin, the final outcome, timestamps, and stable references. It does not
contain a raw token, raw idempotency key, request Markdown, complete request
body, serialized Work projection, copied readiness, or model traffic.

Replaying the same canonical request with the same key returns the same receipt
and stable references, then composes a fresh permission-filtered projection.
Attribution is excluded from the semantic digest: changing only the reported
labels replays the first operation's original attribution without re-executing.
Changing the semantic request while reusing the key returns
`idempotency_conflict` without revealing the earlier target. Mutation results
include required `operation.clientAttribution` with `harness`, optional
`harnessVersion`, `model`, and `source: "client-reported"`. Human Project and
Issue views distinguish principal authority, registered client metadata, and
runtime client-reported annotations. Labels are not attestation or authority;
these views do not expose receipt internals. Deleting a registration does not
rewrite its historical receipt snapshots.

This unreleased receipt schema requires a fresh database. The amended v345 and
v348 sequence provides no upgrade or compatibility path for an already-created
pre-amendment receipt database.

### Work bounds

The `[work]` section contains limits only; it does not enable planning. Defaults
are 1,000 dependency-graph nodes, 1,000 plan items, 100 materialized projection
items, pages of 25 with a maximum of 100, 50 changes and 20 new Issues per plan
revision, 255 title characters, 65,536 Markdown bytes, and a one-MiB serialized
semantic Work result. Unsafe combinations and predictable over-limit requests
are rejected before mutation.

### Deliberate limitations

Work planning does not provide copied or persisted Work state, generic CRUD or
queries, claims, leases, adoption, semantic duplicate detection, verified agent
identity, scheduling, dispatch, worktree creation, execution lifecycle, or
cross-repository planning mutation. Existing readable external prerequisites
remain observable and blocking, but new cross-repository Work dependencies are
not created.

Committed notification, webhook, and indexing effects are dispatched only
after the authoritative transaction and are suppressed on replay. The current
synchronous process-local notifier has no durable acknowledgement across a
process crash between commit and fanout. The
[WP4 post-commit delivery prerequisite](architecture/plans/0004-wp4-post-commit-delivery-prerequisite.md)
therefore remains required before broad rollout or any configuration that
requires crash-safe at-least-once effects. This local Work slice does not add a
ready-work delivery mechanism or a general durable outbox.

## Upgrade, staged enablement, and rollback

Before any supported upgrade, take a tested database backup. The unreleased
fixed-client and pre-amendment receipt database is **not** a supported upgrade
source: discard that disposable substrate and its credentials and reconstruct
from an empty database. Do not run v348 over pre-amendment receipts. The static
empty schema sequence is v344 (disabled Project planning state), amended v345
(receipts and native provenance links), v346 (registration lifecycle), v347
(grant credential-rotation time), and v348 (receipt attribution). Fresh installs
create current models; there is no old-row conversion or compatibility path.

Deploy with the MCP endpoint, client bootstrap, and both Work flags off first.
Verify ordinary Projects, then enable the endpoint for pull inspection and
bootstrap separately for new clients. Enable Work inspection independently if
desired. Opt in only synthetic or explicitly selected repository Projects.
Enable Work mutation only after the OAuth, permission, fault-injection,
database, capacity, output-bound, proxy rate-limit, and recovery evidence has
been reviewed for the deployment. See the
[operator and clean-slate checklist](mcp-operations.md) for configuration,
admission limits, security, and rollback details.

The [WP10 local certification ledger](architecture/plans/0004-wp10-local-certification.md)
records the historical fixed-client slice, not certification of amended
onboarding. The [WP14 acceptance scope](architecture/plans/0004-mcp-work-planning-implementation.md#wp14-amended-onboarding-security-and-dogfood-certification)
requires fresh two-installation evidence and final coordinator review. ADR 0004
remains Proposed; this documentation does not certify a running service.

For an operational rollback on the current schema, disable
`WORK_MUTATION_ENABLED` first. Independently disable `CLIENT_BOOTSTRAP_ENABLED`
to stop new onboarding while approved Read grants continue. Disabling a flag
does not revoke a grant; re-enabling can make retained authority usable again.
Revoke grants when permanent credential invalidation is intended.
Disable `WORK_INSPECTION_ENABLED` next to return
to the ADR 0002 pull-only surface, or disable `ENABLED` to remove the endpoint.
Project state and receipts remain native inert data; disabling an interface does
not rewrite Projects or delete provenance. Do not run an older image against an
upgraded database unless that exact downgrade is documented and tested. The
safe old-image rollback is to stop Forge and restore the pre-upgrade database
backup.

## Pull inspection tool and limits

`pull_request.inspect` identifies one repository and pull request by owner,
repository name, and pull request number. Optional bounded selections expose
changed-file metadata, diff content, checks for the frozen revision, and merge
policy. Metadata includes the repository-authored pull request description as
untrusted raw Markdown with an explicit truncation flag; Forge does not render
it to HTML. Repository enumeration, search, raw files and logs, comments,
reviews, merges, and mutations are not available.

The `[mcp]` settings `MAX_REQUEST_BODY_BYTES` (default one MiB),
`MAX_IN_FLIGHT_REQUESTS` (default 8), and `EXECUTION_TIMEOUT` (default 30
seconds) bound request bodies and semantic work. In-flight admission is
non-blocking. Pull inspection additionally owns a one-MiB semantic MCP tool
result ceiling. Its structured inspection document is limited to 768 KiB; the
remaining space is reserved for the small MCP content block and result
envelope, so structured output is not duplicated as JSON text. This ceiling
does not include JSON-RPC or HTTP framing, whose request identifier and headers
are transport data. A service-owned request budget rejects unsafe combinations
of file, line, text, check, and policy selections before diff materialization;
the individual file, line, text, check, and cursor limits remain in force.
These product limits are intentionally not duplicated in MCP transport
configuration.

Changed-file pages default to 25 files and allow at most 100. Diff pages
default to 10 files, 250 lines per file, and 128 bytes per line. Their
individual maxima are 25 files, 1,000 lines per file, and 10,000 bytes per
line, but combinations must also fit the service-owned request budget. Check
projection is limited to 100 latest contexts, with context, description, and
target URL text limited to 2,000 bytes each.
Pull request descriptions are limited to 32 KiB and truncated on a valid UTF-8
boundary when necessary.

The endpoint is stateless and cross-origin protected. The current experimental
profile relies on an operator-managed reverse proxy as the request-rate-limit
authority. Do not expose it publicly without an explicit, tested proxy rate
limit.
