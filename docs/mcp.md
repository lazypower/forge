# Model Context Protocol

Forge has an experimental, read-only Model Context Protocol (MCP) endpoint. It
is disabled by default and currently provides only the `pull_request.inspect`
tool over stateless Streamable HTTP.

## Enablement and endpoint

Configure an externally correct HTTPS `ROOT_URL`, including any installation
subpath, configure Forge's matching OAuth issuer, and enable the endpoint:

```ini
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

When MCP is enabled with the OAuth profile, Forge registers one built-in public
client named `Forge MCP`, with client ID
`f16c9e54-1f8b-4a9c-9b62-70d8d46f0e31`. Endpoint-disabled installations do
not create this registration merely because OAuth is the default. Once created,
the registration is retained if MCP is later disabled or temporarily rolled
back to PAT. It accepts only the fixed loopback redirects
`http://127.0.0.1`, `http://127.0.0.1/callback`,
`http://127.0.0.1/callback/<callback-id>`, and `https://127.0.0.1`. Forge
derives `<callback-id>` from the canonical MCP resource URL using Codex's
SHA-256 callback binding. The existing public-client rule permits a dynamic
port on the HTTP loopback redirects while preserving their paths. The
`/callback` redirect supports Codex's stable pre-registered client callback;
the derived redirect supports released Codex clients that still append the
server-specific callback ID. Forge advertises
authorization-response issuer support and includes its OAuth issuer in
authorization responses so clients can bind the shared callback to this Forge
instance. The client is not selected through `DEFAULT_APPLICATIONS` and cannot
issue a general Forge API token.

The first startup with a newer callback profile upgrades a recognized earlier
Forge MCP client registration in place. An older Forge release expects its
previous redirect profile and will refuse to start after that upgrade. To roll
back to v1.27.2.7, stop Forge, back up the database, and restore its redirect
set before starting the older image:

```sql
UPDATE oauth2_application
SET redirect_uris = '["http://127.0.0.1","http://127.0.0.1/callback","https://127.0.0.1"]'
WHERE client_id = 'f16c9e54-1f8b-4a9c-9b62-70d8d46f0e31';
```

For releases older than v1.27.2.7, restore the original two-redirect profile:

```sql
UPDATE oauth2_application
SET redirect_uris = '["http://127.0.0.1","https://127.0.0.1"]'
WHERE client_id = 'f16c9e54-1f8b-4a9c-9b62-70d8d46f0e31';
```

This changes only the built-in client's registered redirects; its grants and
tokens are retained.

Authorization requires the exact MCP resource URL, `read:repository` as the
only scope, and PKCE `S256`. The resource is the configured `ROOT_URL`,
including its subpath, followed by `mcp`; the example resource is
`https://forge.example/forge/mcp`. Access and refresh tokens are signed and
bound to that exact audience. Refresh-token use always rotates the existing
grant counter for this client, even when global legacy rotation is disabled.
Consequently, only one refresh lineage per principal and Forge MCP client is
active; two installations can invalidate each other's refresh credentials.
Clients may discard refresh tokens and repeat authorization after access-token
expiry.

OAuth Protected Resource Metadata is served by the official MCP Go SDK at the
application-scoped `/.well-known/oauth-protected-resource/mcp` route and is
advertised explicitly in bearer challenges. Forge's automated interoperability
coverage drives the official MCP Go SDK v1.7.0 client from that challenge
through protected-resource and Forge OpenID Connect discovery, authorization
with the fixed public client and PKCE `S256`, loopback callback, token exchange,
an authenticated `pull_request.inspect` call, access-token refresh, refresh
rotation and replay rejection. It also covers the fixed profile's scope,
audience, credential-profile, unrelated-resource, configured-subpath, and TLS
trust boundaries. This substantiates interoperability for the initial
pre-registered, read-only Forge MCP OAuth profile only. It does not claim
Dynamic Client Registration, Client ID Metadata Documents, external issuer
aliases, per-installation refresh families, mutations, rate limiting, or
broader MCP or OAuth conformance.

## Tool and limits

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
