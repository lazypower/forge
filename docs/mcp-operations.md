# Operating experimental MCP onboarding

This is an operator checklist for the unreleased ADR 0004 amendment. It is not
an acceptance report or authorization to enable a deployment. See the
[client guide](mcp.md) for registration, consent, credentials, and mutation
metadata. All examples use reserved domains and synthetic identities.

## Configuration and staged enablement

Start with the following configuration; merge sections into the existing
configuration rather than adding duplicate sections. Replace the reserved
example URL with the operator's own HTTPS URL, preserving its subpath and the
issuer match. Keep credentials out of this file and out of shared evidence.

```ini
[server]
ROOT_URL = https://forge.example/forge/

[oauth2]
ENABLED = true
JWT_CLAIM_ISSUER = https://forge.example/forge

[mcp]
ENABLED = false
AUTHENTICATION = oauth
CLIENT_BOOTSTRAP_ENABLED = false
WORK_INSPECTION_ENABLED = false
WORK_MUTATION_ENABLED = false
```

After a restart with each intended configuration, check the behavior below.
These are startup settings, not a promise of live configuration reload.

| Setting | Enabled behavior | Disabled behavior |
| --- | --- | --- |
| `ENABLED` | HTTPS MCP endpoint, credential checks, and pull inspection | No MCP endpoint; also set bootstrap false |
| `CLIENT_BOOTSTRAP_ENABLED` | Advertised constrained public-client registration; requires enabled OAuth MCP | New bootstrap unavailable and absent from discovery; existing registrations can still authorize and refresh an enabled profile |
| `WORK_INSPECTION_ENABLED` | Adds Work read tools | Pull inspection remains available with an enabled endpoint |
| `WORK_MUTATION_ENABLED` | Offers the exact Work Planning OAuth profile and three mutation tools under OAuth | No Work Planning issuance, refresh, or invocation; retained grants remain visible as disabled, while Read continues |

Flags do not opt Projects into planning, change native permissions, or revoke
credentials. Re-enabling a profile can make its retained grant usable again;
use Applications **Revoke** for permanent invalidation. A read credential may
discover enabled mutation tools, but discovery confers no write authority.
The temporary PAT profile is read-only and mutually exclusive with OAuth at
MCP; disable bootstrap if selecting it. Do not assume a PAT has OAuth audience
isolation on REST.

## Admission and cleanup

The configuration reference is
[`custom/conf/app.example.ini`](../custom/conf/app.example.ini). Bootstrap uses
an independent budget; it does not consume a Work execution slot.

| Bootstrap setting suffix (`CLIENT_BOOTSTRAP_`) | Default | Startup bound |
| --- | --- | --- |
| `MAX_REQUEST_BODY_BYTES` | 32768 | 1–1048576 bytes |
| `MAX_IN_FLIGHT_REQUESTS` | 4 | 1–64 |
| `PROVISIONAL_LIFETIME` | 30m | 10m–60m |
| `MAX_REDIRECT_URIS` | 5 | 1–20 |
| `MAX_OUTSTANDING` | 1000 | 1–100000 provisional registrations |
| `CLEANUP_BATCH_SIZE` | 100 | Positive, at most 10000 and no greater than outstanding cap |
| `PER_SOURCE_RATE` | 10 | Positive, no greater than instance rate |
| `INSTANCE_RATE` | 100 | 1–100000 per window |
| `MAX_SOURCE_BUCKETS` | 1024 | 1–100000 |
| `RATE_WINDOW` | 1m | 1s–1h |

Rates use fixed windows and the direct connection IP, not caller-supplied
forwarded headers. A reverse proxy may put clients in one shared source bucket;
test that topology privately. Non-IP peers share one bucket. The independently
configured proxy remains the outer request-rate control; do not expose the
experimental endpoint publicly without a tested limit.

Each valid bootstrap attempt performs at most one batch of expired provisional
cleanup before trying to reserve a new registration. There is no scheduled
background cleanup in this slice. With bootstrap disabled or no valid incoming
requests, expired rows can remain stored, still authority-free and bounded by
the cap. Repeated cleanup is safe; it never reaps finalized registrations.
Do not remove application or admission-counter rows with ad hoc SQL.

Provisional expiry during login or consent creates no grant. The client must
bootstrap again. Denying first consent deletes the provisional registration;
denying a profile replacement preserves the existing grant. A source-rotating
caller can fill the instance cap and temporarily deny **new onboarding** until
expiry and cleanup. This is an accepted bounded-storage availability tradeoff,
not protection against every denial of service; established authorization,
refresh, and MCP calls are outside this bootstrap admission budget.

| Bootstrap response | Meaning and recovery |
| --- | --- |
| `201` | Public provisional registration created, still no authority |
| `400 invalid_client_metadata` | Wrong content type, malformed/overlarge body, unsupported fields or invalid closed metadata; fix the request |
| `429 temporarily_unavailable` | Source/instance/source-bucket rate limit or provisional storage cap; back off, respecting `Retry-After` when supplied |
| `503 temporarily_unavailable` | Independent in-flight budget full; back off |
| `404` | Bootstrap is disabled/unavailable; do not fall back to manual application creation |

## Consent and security checks

- Bootstrap must not issue a secret, grant, token, or usable authorization code.
  It accepts no authority selectors, remote logos, software statements, or
  fetched metadata. Labels are bounded untrusted text.
- Only exact HTTPS or literal-IP loopback HTTP redirects are accepted, one
  class per registration. HTTPS callbacks match exactly; loopback may vary only
  the runtime port. Require PKCE S256 and the exact MCP resource throughout
  authorization, code exchange, and refresh. Never disable TLS verification to
  make a client connect.
- Consent shows the requested profile, exact scopes, client-provided/unverified
  labels, and callback context. A familiar name does not prove software
  identity. Profile change needs explicit new consent and invalidates all old
  code, access, and refresh lineages atomically on approval.
- Each independent installation stores its own client ID and credentials even
  when labels match. Revoke one and check the other still reads and refreshes.
  Lost local credentials do not revoke a server grant; reconnect or revoke the
  abandoned authority through Applications.
- MCP OAuth tokens cannot authorize REST, and general OAuth tokens cannot
  authorize MCP. PATs and Read grants cannot mutate Work. Ordinary OAuth
  application management and ordinary disabled Projects retain their existing
  boundaries.
- Required per-operation harness/model attribution is client-reported, never
  a permission input or attestation. Test rejection before receipt lookup or
  resource disclosure. Use the same semantic input and key with different
  labels to verify one native change and the first receipt's original labels.
- Inspect consent, Applications, Project provenance, and Issue history for
  escaping and explicit sources. Do not infer last use from rotation time.
  Application/grant/credential internals, raw keys/tokens, prompts, complete
  mutation requests, and hidden-object data do not belong in shared evidence.
  Limit HTTP/proxy debug logging; never log Authorization or OAuth callback
  query values. Inspect logs privately before retaining a redacted result.

## Clean-slate evaluation and upgrade boundary

The pre-release fixed-client runtime and pre-amendment receipt database have no
supported transition path. This is deliberately different from upgrading a
supported released Forge database. No fixed client alias, application/grant
transfer, receipt backfill, invented attribution, or old credential import is
provided. The existing artifact-deletion policy still minimizes receipt detail
while reserving its key; that is not a pre-amendment compatibility mechanism.

The responsible operator must own the runtime and approve its disposal. For a
fresh evaluation:

1. Record the reviewed build and validate a recovery backup where applicable.
   Stop the disposable old runtime; discard only its approved database and
   credential stores. Do not point test commands at an unrelated service.
2. Create an empty database and new server secrets, data directory, and client
   stores. Do not copy fixed client IDs, grants, authorization codes, access or
   refresh credentials, receipts, or receipt signing material into it.
3. Start the new build with every MCP capability disabled. Record a private
   zero-row check for MCP registrations, grants/codes, and receipts before any
   onboarding. Check the current schema includes v346 registration lifecycle,
   v347 credential-rotation time, and v348 attribution, with receipt indexes
   intact and no former `actor_trust` field. A fresh model schema and the static
   empty amended v345→v348 sequence are the supported test cases; running a
   migration test is not evidence that the live database was rebuilt.
4. Enable only the selected experimental capabilities, then run the amended
   two-installation scenario with synthetic repositories and names. Capture
   first browser consent, settings, profile replacement, revoke/reconnect, and
   Project/Issue provenance. Check bootstrap/mutation disablement separately.
5. Retain exact checks and observed results, clearly distinguishing automated
   tests, fixture renders, and live behavior. Record unavailable databases or
   actions as unproven/not exercised. A setup script or screenshot of a fixture
   does not certify a live onboarding flow.

Do not change migration numbers to make an older binary start. For a rollback
on the current schema, disable mutation first and bootstrap independently;
disable Work inspection or the whole endpoint as needed. Preserve native Work
facts and receipts. For an older binary, stop the service and restore a tested
matching pre-upgrade database/data backup rather than attempting a schema
downgrade. A disposable evaluation can instead be reconstructed from empty
with the selected build and newly authorized clients.

The final clean-slate scenario and ADR-conformance review belong to the
integration coordinator. ADR 0004 remains Proposed until its dependency and
database requirements permit acceptance. The process-local post-commit
notification limitation and its
[delivery prerequisite](architecture/plans/0004-wp4-post-commit-delivery-prerequisite.md)
still block any claim of crash-safe at-least-once effects or broad rollout.
