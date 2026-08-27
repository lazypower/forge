# ADR 0002 Step 4 dogfood evidence

- Date: 2026-08-27
- Tested Forge commit: `f234b56f0f6105cbb094d0a788502a850e0b7c32`
- Scope: ADR 0002 delivery step 4 only

## Environment and client

The tested image was built from the clean Step 3 worktree. Its OCI revision
label reported the tested commit, and the running binary reported
`1.27.2.5+dev-732-f234b56f0f`. The disposable environment used OrbStack 2.2.3,
Docker 29.4.0, and an arm64 macOS host.

Forge ran with `[mcp] ENABLED = true` and `ROOT_URL` set to
`https://forge-mcp-dogfood.orb.local/`. OrbStack terminated locally trusted TLS
for `https://forge-mcp-dogfood.orb.local/mcp` and proxied to the container's
HTTP port. Curl certificate validation returned code 0. The certificate named
the endpoint and chained to the OrbStack Development Root CA.

The client used `github.com/modelcontextprotocol/go-sdk` v1.7.0 with its
stateless Streamable HTTP transport. It negotiated MCP protocol version
`2026-07-28` with server name `forge`. Authentication used a personal access
token stored separately from the endpoint and supplied only as a Bearer header.
The persisted token record had exactly the `read:repository` scope.

## Protocol and permission observations

Official SDK discovery returned exactly one tool:
`pull_request.inspect`. Seven typed calls against a synthetic private pull
request produced the following results:

- Metadata reported an open pull request and nonempty frozen internal-head,
  target, and comparison-base revisions.
- A changed-files request with limit 3 returned a cursor. The next request used
  that cursor and the frozen head and returned a distinct page. The fixture had
  seven changed files.
- A diff request with file limit 2 returned a cursor. The next request used that
  cursor and the frozen head and returned a distinct page.
- Checks were evaluated at the frozen internal head. `ci/unit` was successful.
- Applicable policy was protected, required `ci/unit` and `security/review`,
  reported `security/review` missing, required one approval, and returned three
  blockers.

The HTTPS and authorization boundaries behaved as follows:

- A request without credentials returned HTTP 401 with
  `WWW-Authenticate: Bearer` while TLS validation remained successful.
- The exact `read:repository` PAT completed discovery and all authorized typed
  calls. A broader `all` PAT was rejected during official SDK connection.
- A second principal with its own exact `read:repository` PAT received the
  structured result `{"status":"unavailable"}` for the private pull request.
  The same principal received the identical structured result and generic
  content for a nonexistent pull request. Both were successful tool results,
  rather than distinguishable transport or tool errors.
- The exact MCP PAT could read the pull request, commits, and reviews through
  REST, but an issue-comments request returned HTTP 403. A separately stored
  REST-only PAT scoped to `read:repository,read:issue` completed that fallback.

Token values were never placed in command arguments, URLs, committed files, or
captured output. Protected temporary files supplied credentials to in-memory
headers. Exact-value scans of the server log, worktree, and final diff found no
credential residue before cleanup.

## Engineering workflows

1. **Assess merge readiness.** MCP supplied the frozen revision, changed scope,
   check state, and evaluated policy. The pull request was not ready: one
   required context and one approval were missing. No product fallback was
   needed.
2. **Confirm change intent and scope.** At the tested `f234b56f0f` revision,
   MCP supplied bounded file and diff content, but not the pull request
   description. One REST read confirmed that the synthetic description was
   present.
3. **Audit commit structure.** MCP supplied the aggregate diff but not commit
   provenance. One REST read found seven fixture commits for the seven-file
   change.
4. **Triage review discussion.** MCP supplied approval requirements and blocker
   evaluation but not review records or issue discussion. One REST read found
   no reviews. The issue-comments read required two attempts: the exact MCP PAT
   was denied, and the separate REST-only PAT found no comments.

## Fallback ledger

| Task | Needed fact or action | Fallback used | Occurrences | Classification |
| --- | --- | --- | ---: | --- |
| Runtime and fixture setup | Build the exact revision; create the private PR, check, and policy | Docker/OrbStack and 12 Forge REST mutations | 1 run | `bootstrap/infrastructure only` |
| Runtime verification | Prove revision, HTTPS, configuration, PAT scopes, and cleanup | Shell, Docker inspection, curl, and local database inspection | 1 run | `bootstrap/infrastructure only` |
| Assess merge readiness | Frozen changes, checks, and evaluated policy | None | 0 | `candidate semantic gap` not observed |
| Confirm change intent at `f234b56f0f` | Pull request description | Forge REST pull request read | 1 | `candidate semantic gap` |
| Audit commit structure | Ordered pull request commits | Forge REST commit read | 1 | `candidate semantic gap` |
| Triage review discussion | Review records | Forge REST review read | 1 | `candidate semantic gap` |
| Triage review discussion | Issue comments and their permission boundary | Forge REST issue-comment read; exact PAT denied, REST-only PAT succeeded | 2 | `candidate semantic gap` |
| Submit a review, comment, or merge | Mutation | Not exercised; a write-capable escape hatch would be required | 0 | `intentional escape hatch/non-goal` |

Bootstrap calls establish the environment and fixture; they are not counted as
product-workflow fallbacks. The product workflow made four successful REST
reads and one denied REST attempt because `pull_request.inspect` could not
answer the requested facts.

## Post-dogfood disposition

This evidence remains bound to the tested `f234b56f0f` revision, so its initial
description fallback remains historical fact. After the run, product direction
selected that candidate gap for closure within the existing operation. Commit
`b6418847e98a461b46b32dd5c3b1dd9a0db15a6a` added the repository-authored pull
request description to `pull_request.inspect` as raw Markdown bounded to 32 KiB,
with an explicit `descriptionTruncated` signal. Forge does not render the
description to HTML. Clients at that revision no longer need the recorded REST
fallback for this fact, and no second semantic operation was introduced.

The commit-provenance and review/discussion observations remain recorded.
Reviews and comments are explicit non-goals of the current read-only slice, as
documented in the MCP operator guidance; their appearance in the ledger does not
implicitly select them for implementation.

## Conclusion

Step 4 records candidate gaps; it is neither a veto nor a numerical threshold
for product evolution. Product direction selects which future operations or
extensions Forge should pursue, informed by this evidence and other product
context. The description gap was selected and closed within the existing
operation. Commit provenance and review/discussion remain observations, while
reviews, comments, and their mutations remain current-slice non-goals. This PR
adds no further semantic operation.
