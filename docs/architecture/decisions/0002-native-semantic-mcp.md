# ADR 0002: Provide a native semantic MCP projection

- Status: Implemented
- Date: 2026-08-26
- Decision owner: Forge maintainer
- Depends on: [ADR 0001](0001-agent-native-forge-domain.md)
- Planned partial supersession: [ADR 0004](0004-safe-mcp-work-planning.md) will
  replace the fixed pre-registered MCP clients, shared installation refresh
  lineage, and client registration/onboarding clauses when ADR 0004 is accepted
  and its clean-slate cutover lands. Until then, this implemented decision
  remains the authority for the original read-only slice.

## Context

[ADR 0001](0001-agent-native-forge-domain.md) makes the Forge domain
authoritative for both human and agent collaboration. Agents still need a
standard way to discover and invoke that domain remotely.

Today an agent reaches a Gitea or Forge instance through a local MCP adapter,
command-line client, raw REST calls, or rendered HTML. Those clients carry
forge-specific behavior, reconstruct semantic operations from low-level
resources, and must be updated separately when the server gains a capability.
The server cannot describe its own agent affordances through those adapters.

A native remote MCP endpoint can make a Forge deployment connectable with a
server URL and credential. Its value depends on exposing Forge-owned intent,
rather than reproducing the inherited REST surface under new names.

The security and implementation cost must remain proportional to the first
useful experiment. Comment, review, merge, administration, delegated identity,
general audit, and durable idempotency are separate capabilities. They are not
prerequisites for testing whether bounded semantic inspection is useful.

## Existing authentication substrate

Forge already contains an OAuth 2.0 and OpenID Connect authorization server. It
provides:

- authorization-code grants for public and confidential clients in
  [`OAuth2Application`](../../../models/auth/oauth2.go);
- PKCE validation and one-use authorization codes in
  [`OAuth2AuthorizationCode`](../../../models/auth/oauth2.go);
- registered and built-in OAuth applications;
- OpenID Connect discovery, JWKS, and token introspection in
  [`oauth2_provider.go`](../../../routers/web/auth/oauth2_provider.go);
- signed, short-lived JWT access tokens from
  [`NewAccessTokenResponse`](../../../services/oauth2_provider/access_token.go);
- refresh tokens with configurable expiry; and
- optional refresh-token replay rejection through the grant counter.

OAuth grants already map additional OAuth scopes to Forge access-token scopes.
Those scopes limit the credential while existing Forge permissions determine
which repositories and objects the principal may access. MCP does not need a
parallel scope vocabulary or permission model for its first operations.

Forge's current OAuth access tokens are general API tokens. They do not carry a
resource audience, and the authorization flow does not preserve the OAuth
`resource` parameter required by the MCP authorization profile. Forge also does
not yet publish OAuth Protected Resource Metadata for an MCP endpoint.

Two inherited compatibility behaviors require stricter MCP boundaries.
[`GrantAdditionalScopes`](../../../services/oauth2_provider/access_token.go)
treats an empty or invalid additional scope as full access. The generic
[`OAuth2` authentication method](../../../services/auth/oauth2.go) also accepts
query-string credentials, personal tokens, OAuth tokens, and Actions
credentials. Neither behavior is suitable for the permanent MCP OAuth profile.

Refresh-token invalidation is global, defaults to disabled, and increments one
counter per user and OAuth application. Enabling it can make refresh credentials
from two installations of the same client invalidate each other. Forge has no
per-installation token-family or lease model today.

The official Model Context Protocol Go SDK supplies stateless Streamable HTTP,
bearer parsing, scope and expiry enforcement, baseline authentication
challenges, request-body limits, cancellation propagation, and a Protected
Resource Metadata handler. It does not replace Forge's authorization server or
validate Forge token audiences. Its challenge details must be covered by
interoperability tests rather than assumed complete. Repository inspection
indicates that its dependency requirements should fit Forge's Go toolchain, but
the compatibility spike must prove that claim before adoption.

## Decision

Forge will provide a native MCP server over stateless Streamable HTTP. It will
use the official Model Context Protocol Go SDK for protocol transport and
resource-server authentication affordances unless an implementation spike
finds a concrete incompatibility.

MCP is an agent-facing projection of Forge domain operations. MCP transport
handlers may implement protocol negotiation, request validation,
representation, and error mapping. They must invoke shared application
operations and must not call web or REST handlers.

The MCP surface is a deliberately small, product-owned vocabulary of semantic
operations. It will not be generated from REST routes or database models. Each
invocation should perform enough bounded work to answer one domain-level
intent, avoiding repeated calls to reconstruct a concept Forge already owns.

The endpoint will be disabled by default while experimental. Enabling it must
require an externally correct HTTPS deployment. Credentials must be supplied
separately through client secret storage and must never be embedded in the URL.

## First read-only slice

The first slice will expose one bounded pull request inspection operation. Its
input identifies an owner, repository, and pull request number directly. It may
offer controlled expansions for:

- pull request metadata and a frozen revision set;
- changed-file metadata;
- a bounded diff or diff summary;
- checks associated with the exact revision; and
- configured merge requirements and their evaluated blockers.

The frozen revision set will distinguish the immutable internal pull request
head, target revision, comparison base, optional live source-branch revision,
and merged revision when applicable. Every selected expansion must consume that
captured set rather than resolve a mutable branch again. A source branch that
has advanced independently must be reported as divergent, not silently treated
as the inspected pull request head.

Exact wire names and schemas require implementation design and review. MCP
protocol discovery will describe the server and its implemented capability;
Forge will not add a redundant identification tool merely to restate protocol
metadata.

The first slice will not expose repository enumeration, global search, raw
Actions logs, arbitrary file retrieval, comments, reviews, merges, repository
mutation, or administration. Large collections and diffs require documented
limits, pagination or cursors where appropriate, timeouts, and cancellation.

Changed-file and diff pagination will use the existing bounded diff parser's
`SkipTo`, `Start`, `End`, and `IsIncomplete` behavior in
[`gitdiff`](../../../services/gitdiff/gitdiff.go). An opaque cursor must bind
the next position to the frozen revision set. The MCP projection must not copy
the REST pull-files path, which parses every file with `MaxFiles: -1` before
paginating.

Inspection must be observably read-only. It will not call
`StartPullRequestCheckOnView`, mark an issue read, enqueue a mergeability check,
or perform another web-view side effect. It reports stored state and revision
truth. Refreshing derived state, if later required, will be an explicit
operation.

Reading pull request metadata requires read access to the Pull Requests unit.
Any expansion containing changed files or diff content additionally requires
read access to the Code unit. Missing, private, and denied pull requests must
map to one transport-neutral unavailable result so authorization cannot become
an existence oracle.

The web pull request view and MCP operation must share the extracted
application operation that determines the inspection result. The extraction
should be limited to the useful vertical slice rather than attempting a broad
refactor of inherited web and REST flows.

The shared operation belongs in the pull request service boundary. It will
reuse, rather than replace:

- `GetCompareInfo` for resolved comparison revisions;
- `GetDiffForAPI` and `GetDiffShortStat` for bounded diff mechanics;
- `GetLatestCommitStatus` for latest status per context;
- `EffectiveRequiredContexts` for branch and scoped-workflow requirements;
- `GetFirstMatchProtectedBranchRule` for applicable policy; and
- `GetDoerRepoPermission` for repository and unit authorization.

Router-private check and merge-box structures are presentation models, not
reusable authorities. Their missing-check and evaluated-blocker orchestration
will move into typed, transport-neutral results. Review-policy helpers that
currently log and collapse database errors into conservative blocker values need
error-returning variants for the semantic operation.

The web adapter will deliberately collapse those new errors back into today's
conservative rendering behavior. MCP will return a structured failure instead.
This preserves web behavior without making error suppression part of the shared
domain operation.

Check projection will preserve each status's exact revision and apply the
existing
[`CommitStatusesHideActionsURL`](../../../models/git/commit_status.go) behavior
when the principal cannot read Actions. It will not expose raw job logs or treat
a status target URL as proof that its destination is authorized.

## Authorization delivery

### Experimental bearer-token bootstrap

The read-only dogfood slice may authenticate an existing, explicitly scoped
Forge personal access token. This proves the endpoint and preserves existing
Forge permissions, but it is not the standards-compatible MCP OAuth flow. A
general Forge personal token is not audience-bound and can also be presented to
other Forge API endpoints.

This bootstrap mode must remain disabled by default, read-only, and documented
as experimental. It must accept bearer credentials only in the Authorization
header, never through query parameters, cookies, or Basic authentication.

MCP will use a dedicated token verifier over existing token lookup, scope, user,
and permission authorities. It will not mount Forge's generic API
authentication group. The verifier must reject Actions credentials, HTTP
signatures, reverse-proxy identity, inactive or prohibited users, and every
credential kind not explicitly enabled for the selected MCP authentication
profile. It will validate signed tokens directly rather than reuse Forge's
introspection endpoint, which reports both access and refresh tokens as active.

### MCP OAuth profile

Standards-compatible remote authorization will extend Forge's existing OAuth
provider rather than introduce a second OAuth server or authorization
authority. The implementation will:

1. publish OAuth Protected Resource Metadata for the MCP resource using the
   official SDK handler;
2. point that metadata at Forge's existing OpenID Connect issuer and reject MCP
   enablement when that configured issuer is not discoverable;
3. accept and preserve the OAuth `resource` parameter through authorization,
   token issuance, and refresh;
4. issue short-lived access tokens with issuer, principal subject, and canonical
   MCP resource audience claims;
5. reject tokens at the MCP endpoint when their audience does not match;
6. reject MCP-audience tokens at unrelated Forge resource endpoints;
7. strictly reject empty, unknown, or insufficient MCP-requested scopes instead
   of applying Forge's legacy full-access fallback;
8. return interoperable bearer challenges for missing, expired, incorrectly
   scoped, or incorrectly audience-bound tokens; and
9. require PKCE with `S256` at the authorization server for every MCP-resource
   grant, rejecting absent challenges and the `plain` method.

Pre-registered OAuth clients are sufficient for the initial interoperable
profile and are already supported by Forge. Dynamic Client Registration and
Client ID Metadata Documents are not required for the first client. They may be
added later if interoperability evidence justifies them.

The first pre-registered client application is MCP-resource-exclusive. Forge
will reject authorization or token issuance for that application without the
canonical MCP resource. It cannot issue a general Forge API token, preventing
its grant counter from coupling MCP rotation to a non-MCP token profile.

Forge already issues expiring access and refresh tokens. When MCP authorization
issues a refresh token, the MCP resource profile will force the existing
grant-counter rotation behavior even when global legacy rotation is disabled.
The first profile accepts its resulting limitation: one active refresh lineage
per principal and client application. Multiple installations can invalidate one
another's refresh credentials. A per-installation token-family model requires a
separate decision and is not part of this implementation.

Resource binding requires one new persisted field on the one-use authorization
code. The resource then becomes the immutable audience of the signed refresh
token; refresh validates and carries that audience into replacement tokens. It
does not require a resource field on the longer-lived OAuth grant.

The access token remains short-lived. A client may decline refresh tokens and
repeat the authorization flow after access-token expiry. MCP does not add a
Vault-like lease service or independent credential store.

The experimental PAT bootstrap may ship before this profile. Forge must not
claim MCP OAuth conformance until resource propagation, audience isolation,
discovery, challenges, refresh behavior, and negative security cases are
covered by interoperability tests.

The initial interoperable client will be pre-registered, public, use an HTTPS or
loopback redirect, require PKCE with `S256`, and request a fixed existing
read-only Forge scope. Forge's current grant model does not reliably support
incremental scope escalation, so mutation scopes remain deferred with the
mutations themselves.

## Capability discovery and authorization

Protocol discovery describes what the Forge instance implements. It is
instance-scoped and must not vary to reveal a principal's repository permissions
or access to private resources.

Discovery is not an authorization grant. Forge authorizes every operation
against the authenticated principal, target resource, current state, and
applicable policy when the operation executes. Operation results must not reveal
resources the principal cannot access.

The authenticated OAuth or personal token identifies its Forge principal. The
first read-only slice does not claim to identify a distinct software actor.
Client implementation metadata remains unverified diagnostic information and
must never grant authority.

## Security boundaries

MCP uses existing Forge authentication, token scopes, repository permissions,
protected branches, and policy authorities through shared application
operations.

The official SDK will own the HTTP request-body limit and propagate request
cancellation. Forge will own semantic output bounds, existing Git diff line,
character, and file caps, a tool execution timeout, and a non-blocking
MCP-specific in-flight limit. The timeout bounds one operation's duration; the
in-flight limit bounds concurrent Git, database, and memory consumption. The
first dogfood deployment may rely on an operator-managed reverse proxy for
request-rate limiting because Forge has no general per-principal HTTP rate
limiter. Public exposure requires an explicit, tested rate-limit owner;
cluster-wide rate limiting is not implied by this ADR.

The MCP route will use explicit cross-origin protection. Configurable API CORS
and the SDK's localhost protection are not substitutes for that boundary.

Tool descriptions, repository content, issues, comments, diffs, and other
user-controlled text are untrusted data. Forge will not interpret content as
server instructions or grant capabilities based on it.

Authentication failures must not disclose whether a private repository or pull
request exists. Credentials and authorization codes must not appear in URLs,
logs, error details, tool results, or diagnostic metadata.

## Transport integration grounding

Forge's existing transport infrastructure is sufficient for the endpoint:

- [`NormalRoutes`](../../../routers/init.go) already mounts optional protocol
  routers behind common lifecycle, recovery, security, tracing, and access-log
  middleware;
- [`Response`](../../../services/context/response.go) implements
  `http.Flusher` and tracks response status and bytes;
- request contexts preserve cancellation, deadlines, cleanup, and request-local
  Git repository lifetime;
- the configuration loader supports typed sections and automatic
  `GITEA__MCP__...` environment overrides; and
- the existing `depguard` linter can enforce that MCP projection packages do
  not import web or REST handlers.

The new `[mcp]` section will initially contain `ENABLED=false`, a small request
body limit, maximum in-flight requests, and an execution timeout. Diff and
semantic output limits remain owned by the pull inspection operation and
existing Git settings rather than duplicated as transport configuration.

The implementation spike will select a reviewed release of the official Go SDK
that supports the current stateless MCP protocol and Forge's Go toolchain. As of
this decision, SDK v1.7.0 appears statically compatible with Forge's Go version
and dependency set. Delivery step 1 must prove module resolution, compilation,
and runtime integration. This ADR does not permanently pin that version.

## Delivery sequence

The first delivery will prove the domain seam rather than maximize tool count:

1. Spike the reviewed official Go SDK release with a disabled stateless route
   and no domain tool. Verify protocol discovery, method and content-type rules,
   request-body rejection, cancellation, cross-origin protection, Forge's
   response wrapper, configured subpaths, and maintenance mode.
2. Extract immutable revision resolution and the typed, side-effect-free pull
   request inspection operation. Add bounded files, diff, checks, and policy in
   that order, then move only the corresponding web-view assembly onto it.
3. Add a dedicated bearer-header-only PAT verifier and register exactly one
   read-only inspection tool. Apply execution, in-flight, and output bounds.
4. Dogfood the endpoint and record repeated shell, REST, or adapter fallbacks as
   product evidence before adding another semantic operation.
5. Extract resource-aware OAuth token verification. Existing audience-less REST
   tokens retain their behavior; only the new MCP-audience token class is
   rejected at unrelated resource endpoints.
6. Add Protected Resource Metadata, authorization-code resource persistence,
   audience-bound issuance, endpoint isolation, and the fixed pre-registered
   public client profile.
7. Validate authorization, refresh rotation, scope challenges, and negative
   audience cases with a real MCP client before claiming OAuth conformance.

When the MCP package lands, an automated import-boundary check must prevent it
from importing web or REST router packages. Review convention alone is not a
sufficient architecture boundary.

The implementation must preserve current web, REST, and OAuth behavior.
Existing services may be reused when they already own the required domain
decision. Transport handlers and template preparation code are not reusable
domain boundaries merely because they contain convenient logic.

## Compatibility and evolution

MCP protocol negotiation and Forge's semantic vocabulary are separate
versioning concerns. Forge will advertise the MCP protocol version it speaks
and its implemented semantic capabilities.

Before the first semantic surface is declared stable, incompatible tool-schema
changes are allowed and must be documented. Once stable, existing operations
will evolve compatibly where practical. A materially different meaning should
receive a new operation or explicit semantic version instead of silently
changing an established verb.

The MCP projection is specific to Forge. This decision does not claim a
universal forge ontology or require compatibility with other hosting products.

## Consequences

### Benefits

- The first experiment adds no mutation, idempotency, agent-identity, or general
  audit subsystem.
- The read-only inspection slice adds no durable model or schema migration; the
  later OAuth resource binding adds one authorization-code persistence field.
- Forge reuses its OAuth provider, token scopes, and permissions instead of
  creating competing authorization authorities.
- The official SDK owns MCP protocol framing, bearer parsing, baseline
  challenge construction, request-body limits, cancellation propagation, and
  protected-resource metadata serialization, subject to Forge interoperability
  tests for challenge details.
- Forge reuses its PR, comparison, diff, check, scoped-workflow, branch-policy,
  and permission authorities instead of creating semantic duplicates.
- A useful vertical slice can ship before the OAuth interoperability extension.
- Dogfooding turns real agent friction into direct product evidence.

### Costs and risks

- Even read-only aggregate access expands the private-source disclosure and
  denial-of-service surface.
- The PAT bootstrap permits credential replay against other Forge APIs and is
  unsuitable as the permanent remote authorization design.
- Resource propagation and audience isolation modify security-sensitive OAuth
  code and require focused compatibility and negative tests.
- Existing refresh-token rotation is grant-wide, so two installations using the
  same principal and client can invalidate each other's refresh credentials.
- The official SDK still requires version review and challenge-level
  interoperability tests; library adoption does not delegate Forge's security
  guarantees.
- Router-owned check and policy assembly must be extracted without importing
  web presentation behavior or side effects.
- The initial semantic schema may shape later product vocabulary.

These costs are accepted for a disabled-by-default, bounded experiment because
the implementation builds on existing authorities and can be removed without
migrating durable agent-created state.

## Rejected alternatives

### Continue using an external Gitea MCP adapter

Rejected as Forge's primary agent interface. An external adapter remains useful
as a temporary compatibility path and schema laboratory, but it inherits
version skew and must reconstruct intent from REST resources.

### Generate MCP tools from the REST API

Rejected because it reproduces low-level CRUD, exposes accidental endpoint
structure as product vocabulary, and leaves agents responsible for
reconstructing domain intent.

The existing REST pull-files path is also not an acceptable implementation
shortcut: it disables the parser's file cap and paginates only after parsing the
complete result. MCP will invoke the bounded diff authority directly.

### Reuse the generic API authentication group unchanged

Rejected because it intentionally combines several credential mechanisms and
supports compatibility behavior, including query-string credentials and
Actions identities, that the MCP trust boundary forbids. MCP will reuse token,
scope, user, and permission authorities behind a dedicated bearer verifier.

### Add a second OAuth server library

Rejected because Forge already owns OAuth applications, grants, consent,
scopes, token issuance, refresh, revocation, discovery, signing keys, and
introspection. A second provider would create competing authorities and a
migration burden. The official MCP SDK will provide resource-server integration
while Forge's provider remains the authorization authority.

### Create MCP-specific permissions or OAuth scopes

Rejected for the first surface because existing Forge permissions and
access-token scopes already answer those questions. A new scope is justified
only if a future semantic operation cannot be safely bounded by the existing
model.

### Permanently authenticate with general personal access tokens

Rejected because personal tokens are not restricted to the MCP audience.
Short-lived, audience-bound OAuth access tokens are the intended interoperable
authorization boundary.

### Include mutations in the first slice

Rejected because comments, reviews, merge, and administration require separate
decisions about retries, idempotency, audit, human-visible origin, and trusted
actor attribution. Their absence materially reduces the first slice's security
and persistence footprint.

## Acceptance criteria

The experimental read-only implementation satisfies this decision when:

- an administrator must explicitly enable the MCP endpoint;
- a client can connect with the Forge MCP URL and a separately stored,
  explicitly scoped personal access token;
- an authorized client can inspect one directly identified pull request with
  bounded metadata, diff, check, and applicable-policy information;
- inspection freezes and returns the internal head, target, and comparison-base
  revisions before producing any expansion;
- diff cursors bind their position to that frozen revision set and never parse
  an unbounded file collection before pagination;
- inspection does not enqueue a check, mark an issue read, or otherwise mutate
  state;
- metadata requires Pull Requests read access, while changed-file and diff
  expansions additionally require Code read access;
- unauthorized clients cannot infer private repositories, pull requests, or
  protected related objects;
- missing, private, and denied pull requests use one transport-neutral
  unavailable error;
- the MCP and web projections use the same extracted revision, check, and policy
  inspection operations and existing Forge permission decisions;
- permission-boundary tests cover both the new MCP projection and the migrated
  web inspection path;
- the dedicated verifier rejects query, cookie, Basic, HTTP-signature,
  reverse-proxy, Actions, inactive-user, and unsupported token authentication;
- MCP handlers cannot import web or REST handlers under an automated dependency
  rule;
- no mutating MCP operation is registered;
- request-body, semantic-output, diff, timeout, cancellation, concurrency,
  permission-boundary, and negative security tests cover the exposed surface;
- deployment documentation names the reverse proxy as the rate-limit authority
  until Forge owns an MCP rate limiter; and
- documentation identifies PAT authentication as an experimental bootstrap,
  not standards-compatible MCP OAuth.

The MCP OAuth profile is ready to replace that bootstrap when:

- Forge publishes correct Protected Resource Metadata for the MCP endpoint;
- a pre-registered real MCP client completes authorization against Forge's
  existing OAuth and OpenID Connect endpoints;
- authorization and token requests preserve the canonical MCP resource;
- issued access tokens are short-lived and restricted to that audience;
- MCP, REST, and refresh endpoints reject mismatched resources and token kinds;
- empty, invalid, and insufficient scope requests fail closed;
- MCP-resource authorization rejects absent PKCE challenges and every challenge
  method other than `S256`;
- the pre-registered MCP application cannot issue an audience-less general API
  token;
- inactive and prohibited principals cannot use an otherwise valid token;
- refresh-token replay is rejected when refresh tokens are issued, and the
  grant-wide single-lineage limitation is documented; and
- interoperability and negative security tests substantiate the claimed MCP
  authorization profile.

## Deferred decisions

Separate decisions will define:

- exact wire-level tool names and schemas;
- additional semantic inspection operations;
- client registration beyond pre-registered applications;
- per-installation refresh-token families;
- Forge-owned per-principal or cluster-wide request-rate limiting;
- comment, review, merge, and administration mutations;
- idempotency-key format, storage, retention, and cleanup;
- durable audit and human-visible MCP mutation origin;
- delegated agent enrollment, credential issuance, revocation, and display;
- durable evidence, work claims, proposals, and handoff objects; and
- retention and privacy policy for agent provenance and audit events.

## External references

- [MCP authorization profile][mcp-auth]
- [Official Go SDK protocol and authorization support][go-sdk]
- [Go SDK insufficient-scope challenge issue][sdk-scope-issue]

[mcp-auth]: https://modelcontextprotocol.io/specification/latest
[go-sdk]: https://github.com/modelcontextprotocol/go-sdk/tree/v1.7.0/auth
[sdk-scope-issue]: https://github.com/modelcontextprotocol/go-sdk/issues/1134
