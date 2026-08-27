# Model Context Protocol

Forge has an experimental, read-only Model Context Protocol (MCP) endpoint. It
is disabled by default and currently provides only the `pull_request.inspect`
tool over stateless Streamable HTTP.

## Enablement and endpoint

Configure an externally correct HTTPS `ROOT_URL`, including any installation
subpath, and enable the endpoint:

```ini
ROOT_URL = https://forge.example/forge/

[mcp]
ENABLED = true
```

The endpoint is the configured subpath followed by `/mcp`; the example above is
`https://forge.example/forge/mcp`. Forge refuses to enable MCP when `ROOT_URL`
is not HTTPS.

## Authentication

Create a personal access token whose only scope is `read:repository`. Store the
token in the MCP client's secret storage separately from the server URL, and
send it only as an `Authorization: Bearer …` header. Tokens in URLs, forms,
cookies, Basic authentication, OAuth tokens, and Actions credentials are not
accepted. Do not embed the token in configuration that may be logged or shared.

This PAT bootstrap is experimental and is not MCP OAuth conformance. Forge PATs
do not expire and are not audience-bound, so the same PAT may be accepted by
other Forge API endpoints. OAuth Protected Resource Metadata, MCP resource
audiences, authorization-code resource binding, and the later OAuth delivery
steps are not implemented by this profile.

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

The [ADR 0002 Step 4 dogfood evidence](architecture/evidence/0002-native-semantic-mcp-dogfood.md)
records the first official-client workflow and its fallback ledger.
