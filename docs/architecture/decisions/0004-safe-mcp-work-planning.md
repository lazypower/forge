# ADR 0004: Expose safe semantic work planning through MCP

- Status: Proposed
- Date: 2026-08-28
- Amended: 2026-08-29
- Decision owner: Forge maintainer
- Depends on: [ADR 0001](0001-agent-native-forge-domain.md),
  [ADR 0002](0002-native-semantic-mcp.md),
  [ADR 0003](0003-authoritative-work-planning.md)
- Will partially supersede upon acceptance: ADR 0002's fixed pre-registered
  MCP-client, shared installation refresh-lineage, and deferred
  client-registration clauses
- Implementation plan:
  [ADR 0004 implementation plan](../plans/0004-mcp-work-planning-implementation.md)

## Context

[ADR 0003](0003-authoritative-work-planning.md) is the authoritative work
domain. It fixes the native Project, Issue, Project-Issue relation, Issue
dependency, pull request cross-reference, revision, and commit-status facts
from which Forge composes `WorkPlan`, `WorkItem`, and `PlanContext`. This
decision does not reopen that mapping, persist the projections, or add an
execution owner.

[ADR 0002](0002-native-semantic-mcp.md) established a stateless Streamable HTTP
MCP endpoint and one read-only semantic pull request tool. Planning mutations
need stronger boundaries than that first experiment: a lost response must not
duplicate an Issue, a stale client must not overwrite accepted content, a
multi-row plan edit must not partly commit, and a credential must not acquire
write authority merely because the endpoint can describe a write tool.

The inherited implementation supplies important foundations, but not this
complete envelope:

- [`routers/mcp`](../../../routers/mcp) owns the endpoint, strict bearer
  verification, request bounds, and the existing semantic tool pattern;
- [`services/oauth2_provider`](../../../services/oauth2_provider) owns the
  audience-bound MCP OAuth profile;
- [`services/pull/inspection.go`](../../../services/pull/inspection.go) shows
  permission-aware projection, frozen revision, signed cursor, timeout, and
  non-disclosing result behavior;
- [`models/db/context.go`](../../../models/db/context.go) has ordinary nested
  transactions but no cross-backend serializable transaction with bounded
  retry;
- [`models/issues/dependency.go`](../../../models/issues/dependency.go) rejects
  only duplicates and direct reciprocal edges, not cycles of every length;
- [`services/issue`](../../../services/issue) commonly persists first and emits
  notifications afterward, which must be separated into transactional state
  and post-commit effects before a larger atomic revision can reuse it; and
- OAuth grants are unique per user and application. The initial fixed read and
  write applications collapse every harness installation into shared labels
  and refresh lineages, while the existing grant page exposes neither the
  exact authority nor enough client context for a principal to understand what
  they can revoke.

MCP clients also need a stable contract. Generic Project and Issue CRUD would
force clients to reconstruct the work domain and its invariants. A generic
batch language would turn Forge into a workflow interpreter. A very fine tool
set would make common plan construction non-atomic. The contract therefore
needs a small semantic vocabulary with an intentionally bounded compound
operation.

## Decision

Forge will expose the ADR 0003 work domain through five MCP tools:

| Tool | Class | Purpose |
| --- | --- | --- |
| `work_item.inspect` | Read | Compose one Issue-centered `WorkItem` |
| `work_plan.inspect` | Read | Compose one repository `WorkPlan` page |
| `work_plan.begin` | Mutation | Create a draft plan or opt in a Project |
| `work_item.revise` | Mutation | Conditionally revise or change one Issue |
| `work_plan.revise` | Mutation | Atomically apply bounded planning changes |

These tools call the same domain operations as human interfaces. They do not
expose generic Project or Issue CRUD, arbitrary queries, an operation scripting
language, or a generic projection engine. MCP is an interface origin, not a
second work authority.

Forge remains a forge and coordination service. It does not adopt work for an
agent, select an executor, dispatch a harness, schedule or retry execution,
register agents or models, or infer exclusive delivery ownership. Claims,
leases, semantic duplicate detection, and copied Work state remain outside
this decision.

This decision also extends ADR 0002's OAuth profile for usable multi-client
onboarding. Each independently revocable harness installation obtains one
durable, public, MCP-exclusive OAuth client registration through a constrained
programmatic bootstrap. The bootstrap is plumbing, not another human ceremony:
the visible flow remains client request, browser login, explicit profile and
scope consent, callback, token exchange, and silent refresh until revocation or
a new scope decision. A registration carries no authority before a principal
approves a grant.

Forge distinguishes facts it can enforce from labels useful for later
archaeology. The principal, client registration, grant, exact scopes, audience,
credential, operation, and outcome are authoritative Forge records. Client and
installation labels are registered metadata. MCP harness and model labels are
client-reported operation metadata. Neither label class identifies a security
principal or gains authority, and Forge does not maintain a harness or model
registry.

### Protocol and schema rules

Every tool uses MCP `inputSchema` and `outputSchema` with JSON Schema 2020-12.
The server returns the output as `structuredContent` and also emits a short text
summary for clients that do not render structured output. Schema version `1`
is a string so a later incompatible contract can use a new tool or schema
version without interpreting a decimal as a revision counter.

Unknown input fields are rejected. Strings are UTF-8. Numbers are JSON integers
within Go's signed 64-bit range. The standalone schema registered for each tool
expands the following normative definitions; `$ref` below is editorial
shorthand and never requires a client-side schema fetch.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$defs": {
    "repository": {
      "type": "object",
      "additionalProperties": false,
      "required": ["owner", "name"],
      "properties": {
        "owner": {"type": "string", "minLength": 1, "maxLength": 255},
        "name": {"type": "string", "minLength": 1, "maxLength": 255}
      }
    },
    "page": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "limit": {"type": "integer", "minimum": 1, "maximum": 100,
          "default": 25},
        "cursor": {"type": "string", "minLength": 1, "maxLength": 2048}
      }
    },
    "resultStatus": {
      "type": "string",
      "enum": ["available", "unavailable", "error"]
    },
    "issueRef": {
      "type": "string",
      "pattern": "^issue/[1-9][0-9]*$"
    },
    "projectRef": {
      "type": "string",
      "pattern": "^project/[1-9][0-9]*$"
    },
    "contextRef": {
      "type": "string",
      "pattern": "^project/[1-9][0-9]*/issue/[1-9][0-9]*$"
    },
    "idempotencyKey": {
      "type": "string",
      "pattern": "^[!-~]{16,128}$"
    },
    "clientAttribution": {
      "type": "object",
      "additionalProperties": false,
      "required": ["harness", "source"],
      "properties": {
        "harness": {"type": "string", "minLength": 1,
          "maxLength": 128},
        "harnessVersion": {"type": "string", "minLength": 1,
          "maxLength": 64},
        "model": {"type": "string", "minLength": 1,
          "maxLength": 128},
        "source": {"const": "client-reported"}
      }
    }
  }
}
```

The server generates references and accepts only their canonical form:

- `issue/<Issue number>` identifies a non-pull Issue in the named repository;
- `project/<Project ID>` identifies a repository Project; and
- `project/<Project ID>/issue/<Issue number>` identifies one plan context.

Positive base-10 numbers have no sign or leading zero. An Issue reference uses
the human-visible repository Issue number, never its database ID. A Project
reference uses the Project ID because Projects have no repository-local human
number. A reference is meaningful only with the canonical repository returned
by Forge. Repository renames change the repository locator but not the numeric
object identities. Outputs include the repository's canonical HTML URL and
stable object URLs; clients must not construct URLs from references.

### Read tools

`work_item.inspect` has this exact input shape:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["repository", "workItem"],
  "properties": {
    "repository": {"$ref": "#/$defs/repository"},
    "workItem": {"$ref": "#/$defs/issueRef"},
    "selectedPlan": {"$ref": "#/$defs/projectRef"},
    "pageKind": {
      "type": "string",
      "enum": ["prerequisites", "dependents", "memberships", "contexts",
        "deliveries"],
      "default": "contexts"
    },
    "page": {"$ref": "#/$defs/page"}
  }
}
```

`work_plan.inspect` has this exact input shape:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["repository", "workPlan"],
  "properties": {
    "repository": {"$ref": "#/$defs/repository"},
    "workPlan": {"$ref": "#/$defs/projectRef"},
    "pageKind": {
      "type": "string",
      "enum": ["items", "edges", "ready_frontier", "excluded_members"],
      "default": "items"
    },
    "page": {"$ref": "#/$defs/page"}
  }
}
```

Both read tools return this envelope:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["schemaVersion", "status"],
  "properties": {
    "schemaVersion": {"const": "1"},
    "status": {"$ref": "#/$defs/resultStatus"},
    "repository": {"$ref": "#/$defs/repositoryResult"},
    "workItem": {"$ref": "#/$defs/workItemResult"},
    "workPlan": {"$ref": "#/$defs/workPlanResult"},
    "selectedContext": {"$ref": "#/$defs/planContextResult"},
    "page": {"$ref": "#/$defs/pageResult"},
    "problem": {"$ref": "#/$defs/problem"}
  },
  "allOf": [
    {
      "if": {"properties": {"status": {"const": "available"}}},
      "then": {
        "required": ["repository", "page"],
        "oneOf": [
          {
            "required": ["workItem"],
            "not": {"required": ["workPlan"]}
          },
          {
            "required": ["workPlan"],
            "not": {
              "anyOf": [
                {"required": ["workItem"]},
                {"required": ["selectedContext"]}
              ]
            }
          }
        ],
        "not": {"required": ["problem"]}
      }
    },
    {
      "if": {"properties": {"status": {"const": "unavailable"}}},
      "then": {
        "not": {
          "anyOf": [
            {"required": ["repository"]},
            {"required": ["workItem"]},
            {"required": ["workPlan"]},
            {"required": ["selectedContext"]},
            {"required": ["page"]},
            {"required": ["problem"]}
          ]
        }
      }
    },
    {
      "if": {"properties": {"status": {"const": "error"}}},
      "then": {
        "required": ["problem"],
        "not": {
          "anyOf": [
            {"required": ["repository"]},
            {"required": ["workItem"]},
            {"required": ["workPlan"]},
            {"required": ["selectedContext"]},
            {"required": ["page"]}
          ]
        }
      }
    }
  ]
}
```

Exactly one of `workItem` or `workPlan` is present for `available`; an item may
also contain `selectedContext`. `unavailable` contains no repository or work
object. `error` contains only `problem` and any non-sensitive retry guidance.
The concrete result definitions mirror the ADR 0003 projection exactly:

| Result | Required fields |
| --- | --- |
| `repositoryResult` | `owner`, `name`, `url` |
| `workItemResult` | `ref`, `url`, `title`, `markdown`, `contentVersion`, `state`, `classification`, `contextSummaries`, `projectMemberships`, `prerequisiteSummaries`, `dependentSummaries`, `deliverySummaries` |
| `workPlanResult` | `ref`, `url`, `title`, `markdown`, `planningState`, `projectState`, `integrity`, `itemSummaries`, `edgeSummaries`, `readyFrontier`, `excludedMembers`, `planToken` |
| `planContextResult` | `ref`, `workPlan`, `workItem`, `derivedState`, `integrity`, `prerequisiteSummaries`, `deliverySummaries` |
| `pageResult` | `kind`, `items`, `snapshotConsistency`, `reinspectBeforeAction`; optional `nextCursor` |
| `problem` | `code`, `message`, `retryable`, optional `retryAfterMilliseconds` |

The referenced result definitions are closed objects. Their exact shared
shapes are:

```json
{
  "$defs": {
    "repositoryResult": {
      "type": "object",
      "additionalProperties": false,
      "required": ["owner", "name", "url"],
      "properties": {
        "owner": {"type": "string"},
        "name": {"type": "string"},
        "url": {"type": "string", "format": "uri"}
      }
    },
    "availability": {
      "type": "string",
      "enum": ["available", "undisclosed"]
    },
    "referenceRef": {
      "oneOf": [
        {"$ref": "#/$defs/issueRef"},
        {"$ref": "#/$defs/projectRef"},
        {"$ref": "#/$defs/contextRef"},
        {"type": "string", "pattern": "^pull/[1-9][0-9]*$"}
      ]
    },
    "referenceSummary": {
      "type": "object",
      "additionalProperties": false,
      "required": ["availability"],
      "properties": {
        "availability": {"$ref": "#/$defs/availability"},
        "repository": {"$ref": "#/$defs/repositoryResult"},
        "ref": {"$ref": "#/$defs/referenceRef"},
        "url": {"type": "string", "format": "uri"},
        "label": {"type": "string"},
        "state": {
          "enum": ["open", "closed", "merged", "disabled", "draft",
            "active", "planned", "ready", "blocked", "complete",
            "excluded"]
        }
      },
      "allOf": [
        {
          "if": {"properties": {"availability": {"const": "available"}}},
          "then": {
            "required": ["repository", "ref", "url", "label", "state"]
          }
        },
        {
          "if": {"properties": {"availability": {"const": "undisclosed"}}},
          "then": {
            "not": {
              "anyOf": [
                {"required": ["ref"]},
                {"required": ["repository"]},
                {"required": ["url"]},
                {"required": ["label"]},
                {"required": ["state"]}
              ]
            }
          }
        }
      ]
    },
    "integrity": {
      "type": "object",
      "additionalProperties": false,
      "required": ["status", "concerns"],
      "properties": {
        "status": {"enum": ["valid", "concern", "incomplete"]},
        "concerns": {
          "type": "array",
          "maxItems": 100,
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["code", "message"],
            "properties": {
              "code": {"type": "string"},
              "message": {"type": "string"}
            }
          }
        }
      }
    },
    "deliverySummary": {
      "type": "object",
      "additionalProperties": false,
      "required": ["repository", "ref", "url", "state", "revision",
        "checkState"],
      "properties": {
        "repository": {"$ref": "#/$defs/repositoryResult"},
        "ref": {"type": "string", "pattern": "^pull/[1-9][0-9]*$"},
        "url": {"type": "string", "format": "uri"},
        "state": {"enum": ["open", "closed", "merged"]},
        "revision": {"type": "string", "pattern": "^[0-9a-f]{40,64}$"},
        "checkState": {
          "enum": ["success", "failure", "pending", "unverified", "none"]
        }
      }
    },
    "contextSummary": {
      "type": "object",
      "additionalProperties": false,
      "required": ["ref", "workPlan", "derivedState", "integrityStatus"],
      "properties": {
        "ref": {"$ref": "#/$defs/contextRef"},
        "workPlan": {"$ref": "#/$defs/projectRef"},
        "derivedState": {
          "enum": ["planned", "ready", "blocked", "complete"]
        },
        "integrityStatus": {"enum": ["valid", "concern", "incomplete"]}
      }
    },
    "workItemResult": {
      "type": "object",
      "additionalProperties": false,
      "required": ["ref", "url", "title", "markdown", "contentVersion",
        "state", "classification", "contextSummaries",
        "projectMemberships", "prerequisiteSummaries",
        "dependentSummaries", "deliverySummaries"],
      "properties": {
        "ref": {"$ref": "#/$defs/issueRef"},
        "url": {"type": "string", "format": "uri"},
        "title": {"type": "string"},
        "markdown": {"type": "string"},
        "contentVersion": {"type": "integer", "minimum": 0},
        "state": {"enum": ["open", "closed"]},
        "classification": {"enum": ["unplanned", "planned"]},
        "contextSummaries": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/contextSummary"}
        },
        "projectMemberships": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/referenceSummary"}
        },
        "prerequisiteSummaries": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/referenceSummary"}
        },
        "dependentSummaries": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/referenceSummary"}
        },
        "deliverySummaries": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/deliverySummary"}
        }
      }
    },
    "planContextResult": {
      "type": "object",
      "additionalProperties": false,
      "required": ["ref", "workPlan", "workItem", "derivedState",
        "integrity", "prerequisiteSummaries", "deliverySummaries"],
      "properties": {
        "ref": {"$ref": "#/$defs/contextRef"},
        "workPlan": {"$ref": "#/$defs/projectRef"},
        "workItem": {"$ref": "#/$defs/issueRef"},
        "derivedState": {
          "enum": ["planned", "ready", "blocked", "complete"]
        },
        "integrity": {"$ref": "#/$defs/integrity"},
        "prerequisiteSummaries": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/referenceSummary"}
        },
        "deliverySummaries": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/deliverySummary"}
        }
      }
    },
    "workPlanResult": {
      "type": "object",
      "additionalProperties": false,
      "required": ["ref", "url", "title", "markdown", "planningState",
        "projectState", "integrity", "itemSummaries", "edgeSummaries",
        "readyFrontier", "excludedMembers", "planToken"],
      "properties": {
        "ref": {"$ref": "#/$defs/projectRef"},
        "url": {"type": "string", "format": "uri"},
        "title": {"type": "string"},
        "markdown": {"type": "string"},
        "planningState": {"enum": ["draft", "active"]},
        "projectState": {"enum": ["open", "closed"]},
        "integrity": {"$ref": "#/$defs/integrity"},
        "itemSummaries": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/contextSummary"}
        },
        "edgeSummaries": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/edgeSummary"}
        },
        "readyFrontier": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/contextSummary"}
        },
        "excludedMembers": {
          "type": "array", "maxItems": 100,
          "items": {"$ref": "#/$defs/referenceSummary"}
        },
        "planToken": {"type": "string", "minLength": 1, "maxLength": 2048}
      }
    },
    "edgeSummary": {
      "type": "object",
      "additionalProperties": false,
      "required": ["blocked", "prerequisite"],
      "properties": {
        "blocked": {"$ref": "#/$defs/referenceSummary"},
        "prerequisite": {"$ref": "#/$defs/referenceSummary"}
      }
    },
    "pageResult": {
      "type": "object",
      "additionalProperties": false,
      "required": ["kind", "items", "snapshotConsistency",
        "reinspectBeforeAction"],
      "properties": {
        "kind": {
          "enum": ["prerequisites", "dependents", "memberships", "contexts",
            "deliveries", "items", "edges", "ready_frontier",
            "excluded_members"]
        },
        "items": {
          "type": "array",
          "maxItems": 100,
          "items": {
            "oneOf": [
              {"$ref": "#/$defs/referenceSummary"},
              {"$ref": "#/$defs/contextSummary"},
              {"$ref": "#/$defs/deliverySummary"},
              {"$ref": "#/$defs/edgeSummary"}
            ]
          }
        },
        "nextCursor": {"type": "string", "maxLength": 2048},
        "snapshotConsistency": {"const": "none"},
        "reinspectBeforeAction": {"const": true}
      },
      "allOf": [
        {
          "if": {
            "properties": {
              "kind": {
                "enum": ["prerequisites", "dependents", "memberships",
                  "excluded_members"]
              }
            }
          },
          "then": {
            "properties": {
              "items": {"items": {"$ref": "#/$defs/referenceSummary"}}
            }
          }
        },
        {
          "if": {
            "properties": {
              "kind": {"enum": ["contexts", "items", "ready_frontier"]}
            }
          },
          "then": {
            "properties": {
              "items": {"items": {"$ref": "#/$defs/contextSummary"}}
            }
          }
        },
        {
          "if": {"properties": {"kind": {"const": "deliveries"}}},
          "then": {
            "properties": {
              "items": {"items": {"$ref": "#/$defs/deliverySummary"}}
            }
          }
        },
        {
          "if": {"properties": {"kind": {"const": "edges"}}},
          "then": {
            "properties": {
              "items": {"items": {"$ref": "#/$defs/edgeSummary"}}
            }
          }
        }
      ]
    },
    "problem": {
      "type": "object",
      "additionalProperties": false,
      "required": ["code", "message", "retryable"],
      "properties": {
        "code": {"type": "string"},
        "message": {"type": "string"},
        "retryable": {"type": "boolean"},
        "retryAfterMilliseconds": {"type": "integer", "minimum": 1}
      }
    }
  }
}
```

`state` is `open` or `closed`; `classification` is `unplanned` or `planned`;
`planningState` is `draft` or `active`; `projectState` is `open` or `closed`;
and `derivedState` is `planned`, `ready`, `blocked`, or `complete`. Integrity
has `valid`, `concern`, or `incomplete` status and bounded, permission-filtered
concerns. Delivery summaries contain only pull request reference and URL,
state, exact frozen or merged revision, and combined base-repository check
state. They do not embed diffs, reviews, individual checks, or logs.

Every available reference summary contains the canonical repository result,
stable reference within that repository, URL, display label, and minimum state
needed by ADR 0003. This qualification also defines a readable external
prerequisite: its `repository` names the external repository and its `ref` is
interpreted there, not in the request repository. A hidden prerequisite is one
summary with `availability: "undisclosed"` and no repository, reference, URL,
label, or state. Detailed definitions and generated Go types are one authority
in the MCP package and must be schema-tested against work service results;
transport handlers may not invent parallel projection semantics.

Delivery `checkState` uses current base-repository commit-status combination:

| Native base-repository evidence | MCP `checkState` |
| --- | --- |
| At least one `error`, `failure`, or `warning` | `failure` |
| No failure and at least one `pending` | `pending` |
| Every latest context is `success` or `skipped` | `success` |
| No contexts on a same-repository delivery revision | `none` |
| No base-repository evidence for a fork-sourced delivery revision | `unverified` |

This mapping uses the latest status per context and the existing
[`CommitStatusStates.Combine`](../../../modules/commitstatus/commit_status.go)
authority after distinguishing zero contexts and fork provenance. It never
combines fork-only statuses into base-repository evidence.

`snapshotConsistency` is always `"none"`, and
`reinspectBeforeAction` is always `true`. A signed, versioned cursor binds the
repository ID, top-level object, page kind, ordering, and last returned Issue.
It does not freeze a just-in-time projection or bypass fresh permission checks.

Read tools are observably read-only. Their MCP annotations set `readOnlyHint`
and `idempotentHint` true and `openWorldHint` false.

### Mutation tools

All mutation tools require `idempotencyKey`. A key is opaque printable ASCII,
16 through 128 bytes, and must be generated with high entropy. It is not a
work reference or a user-visible name.

Every mutation request requires standard MCP client attribution. Forge reads
the harness name and version from standard MCP
`clientInfo`, using the negotiated per-request value or the legacy initialized
session value. The optional request `_meta` key
`io.gitea.forge/clientAttribution` has this closed shape:

```json
{
  "model": "gpt-5.6"
}
```

`clientInfo.name` is required, trimmed, and non-empty. When the Forge-specific
object is present, it must contain exactly one non-empty `model`; when absent,
Forge records no model. Both labels are client-reported strings of at most 128
UTF-8 characters without control characters.
`clientInfo.version`, when present, has the same rules and a 64-character
limit. Missing or malformed standard client information, or malformed or
over-bound supplied Forge metadata, fails before the Work domain service,
receipt lookup, or native mutation with
`client_attribution_required`; the failure is non-retryable until the client
corrects the request. This is an MCP provenance-profile failure, not a Work
domain rejection or an authorization fact.

Attribution metadata is deliberately outside the canonical semantic request
and idempotency digest. The first committed receipt preserves the original
client attribution. Replaying the same semantic request and key
returns that original attribution even if a later transport request reports
different labels. Labels never select permissions, profiles, repositories, or
operation behavior.

`work_plan.begin` creates a new repository Project in draft planning state or
opts an existing disabled Project into draft:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["repository", "idempotencyKey", "begin"],
  "properties": {
    "repository": {"$ref": "#/$defs/repository"},
    "idempotencyKey": {"$ref": "#/$defs/idempotencyKey"},
    "begin": {
      "oneOf": [
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["kind", "title"],
          "properties": {
            "kind": {"const": "new"},
            "title": {"type": "string", "minLength": 1,
              "maxLength": 255},
            "markdown": {"type": "string", "maxLength": 65536}
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["kind", "workPlan"],
          "properties": {
            "kind": {"const": "existing"},
            "workPlan": {"$ref": "#/$defs/projectRef"}
          }
        }
      ]
    }
  }
}
```

An existing Project must be in `disabled` planning state. Beginning a plan does
not activate it and never adopts work, selects an executor, or infers member
Issues from its prose.

`work_item.revise` conditionally changes title, Markdown, or open/closed state:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["repository", "workItem", "idempotencyKey"],
  "properties": {
    "repository": {"$ref": "#/$defs/repository"},
    "workItem": {"$ref": "#/$defs/issueRef"},
    "idempotencyKey": {"$ref": "#/$defs/idempotencyKey"},
    "title": {
      "type": "object",
      "additionalProperties": false,
      "required": ["expected", "desired"],
      "properties": {
        "expected": {"type": "string", "maxLength": 255},
        "desired": {"type": "string", "minLength": 1,
          "maxLength": 255}
      }
    },
    "markdown": {
      "type": "object",
      "additionalProperties": false,
      "required": ["expectedContentVersion", "desired"],
      "properties": {
        "expectedContentVersion": {"type": "integer", "minimum": 0},
        "desired": {"type": "string", "maxLength": 65536}
      }
    },
    "state": {
      "type": "object",
      "additionalProperties": false,
      "required": ["desired"],
      "properties": {
        "desired": {"type": "string", "enum": ["open", "closed"]}
      }
    }
  },
  "anyOf": [
    {"required": ["title"]},
    {"required": ["markdown"]},
    {"required": ["state"]}
  ]
}
```

Title and Markdown preconditions are both checked before either changes. State
is a desired-state operation: repeating close or reopen is unchanged, while
existing dependency closure rules still apply.

`work_plan.revise` applies one bounded, plan-centered revision:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["repository", "workPlan", "idempotencyKey", "changes"],
  "properties": {
    "repository": {"$ref": "#/$defs/repository"},
    "workPlan": {"$ref": "#/$defs/projectRef"},
    "idempotencyKey": {"$ref": "#/$defs/idempotencyKey"},
    "expectedPlanToken": {"type": "string", "minLength": 1,
      "maxLength": 2048},
    "changes": {
      "type": "array",
      "minItems": 1,
      "maxItems": 50,
      "items": {"$ref": "#/$defs/planChange"}
    }
  }
}
```

`planChange` is exactly one of these closed variants:

| `kind` | Required fields | Meaning |
| --- | --- | --- |
| `ensure_member` | `workItem`, `presence` | Make one existing Issue membership `present` or `absent` |
| `create_member` | `localReference`, `title`, optional `markdown` | Create one Issue and add it to this plan |
| `ensure_dependency` | `blocked`, `prerequisite`, `presence` | Make one dependency edge `present` or `absent` |
| `set_planning_state` | `expected`, `desired` | Change `draft` to `active` or `active` to `draft` |
| `delete_draft` | none | Delete this draft Project, leaving Issues intact |

The exact change union is:

```json
{
  "$defs": {
    "itemSelector": {
      "oneOf": [
        {"$ref": "#/$defs/issueRef"},
        {"type": "string", "pattern": "^local/[A-Za-z][A-Za-z0-9_-]{0,63}$"}
      ]
    },
    "planChange": {
      "oneOf": [
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["kind", "workItem", "presence"],
          "properties": {
            "kind": {"const": "ensure_member"},
            "workItem": {"$ref": "#/$defs/itemSelector"},
            "presence": {"enum": ["present", "absent"]}
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["kind", "localReference", "title"],
          "properties": {
            "kind": {"const": "create_member"},
            "localReference": {
              "type": "string",
              "pattern": "^[A-Za-z][A-Za-z0-9_-]{0,63}$"
            },
            "title": {"type": "string", "minLength": 1,
              "maxLength": 255},
            "markdown": {"type": "string", "maxLength": 65536}
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["kind", "blocked", "prerequisite", "presence"],
          "properties": {
            "kind": {"const": "ensure_dependency"},
            "blocked": {"$ref": "#/$defs/itemSelector"},
            "prerequisite": {"$ref": "#/$defs/itemSelector"},
            "presence": {"enum": ["present", "absent"]}
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["kind", "expected", "desired"],
          "properties": {
            "kind": {"const": "set_planning_state"},
            "expected": {"enum": ["draft", "active"]},
            "desired": {"enum": ["draft", "active"]}
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["kind"],
          "properties": {"kind": {"const": "delete_draft"}}
        }
      ]
    }
  }
}
```

`workItem`, `blocked`, and `prerequisite` accept an `issueRef` or
`local/<localReference>` for an earlier creation in the same request. A
`localReference` matches
`^[A-Za-z][A-Za-z0-9_-]{0,63}$`, is unique within the request, and is returned
with its generated `issueRef`. At most 20 `create_member` changes are allowed.
Every dependency endpoint must be a non-pull Issue in the same repository; new
cross-repository plan edges are deferred.

`presence` is `present` or `absent`. Membership and dependency changes are
set-oriented and converge without a whole-plan precondition. A request that
contains `set_planning_state` or `delete_draft` must provide
`expectedPlanToken`. `delete_draft` must be the only change. A revision may have
at most one planning-state change. Duplicate or contradictory changes to the
same member, edge, or planning state are invalid input rather than order-
dependent instructions.

`planToken` is an opaque, signed, short-lived digest computed just in time from
the Project's authoritative planning and lifecycle state, current membership,
dependency graph, and Issue states relevant to activation or deletion. It is
not stored and is not a Work revision. The server recomputes it inside the
serializable transaction. A stale token yields `conflict` without revealing
which hidden fact changed. Set-only revisions do not need a token, but still
revalidate all ADR 0003 invariants within the transaction.

The three mutation tools return this exact envelope:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["schemaVersion", "status"],
  "properties": {
    "schemaVersion": {"const": "1"},
    "status": {"enum": ["applied", "unchanged", "rejected", "error"]},
    "operation": {
      "type": "object",
      "additionalProperties": false,
      "required": ["id", "replayed", "changed", "committedAt",
        "clientAttribution"],
      "properties": {
        "id": {"type": "string", "format": "uuid"},
        "replayed": {"type": "boolean"},
        "changed": {"type": "boolean"},
        "committedAt": {"type": "string", "format": "date-time"},
        "clientAttribution": {"$ref": "#/$defs/clientAttribution"}
      }
    },
    "createdReferences": {
      "type": "object",
      "additionalProperties": {"$ref": "#/$defs/issueRef"}
    },
    "workItem": {"$ref": "#/$defs/workItemResult"},
    "workPlan": {"$ref": "#/$defs/workPlanResult"},
    "selectedContext": {"$ref": "#/$defs/planContextResult"},
    "currentResultStatus": {
      "enum": ["available", "unavailable", "projection_unavailable"]
    },
    "problem": {"$ref": "#/$defs/problem"}
  },
  "allOf": [
    {
      "if": {
        "properties": {
          "status": {"enum": ["applied", "unchanged"]}
        }
      },
      "then": {
        "required": ["operation", "currentResultStatus"],
        "not": {"required": ["problem"]}
      }
    },
    {
      "if": {"properties": {"status": {"const": "rejected"}}},
      "then": {"required": ["operation", "problem"]}
    },
    {
      "if": {"properties": {"status": {"const": "error"}}},
      "then": {
        "required": ["problem"],
        "not": {
          "anyOf": [
            {"required": ["operation"]},
            {"required": ["createdReferences"]},
            {"required": ["workItem"]},
            {"required": ["workPlan"]},
            {"required": ["selectedContext"]},
            {"required": ["currentResultStatus"]}
          ]
        }
      }
    }
  ]
}
```

`applied` and `unchanged` are committed outcomes and have no `problem`.
`rejected` is a durable, deterministic domain rejection and includes `problem`.
These three statuses contain the final receipt as `operation`. `error`
represents no known committed outcome, contains `problem`, and has no outcome
or projection fields. A successful result includes the applicable fresh,
permission-filtered `WorkItem`,
`WorkPlan`, or selected `PlanContext`; `createdReferences` resolves local
references. If post-commit composition times out or access has since changed,
the committed operation remains successful and `currentResultStatus` says why
the current projection is absent. Forge never stores a serialized projection
for replay.

Mutation annotations set `readOnlyHint` false, `idempotentHint` true, and
`openWorldHint` false. `destructiveHint` is false for `work_plan.begin`; it is
true for `work_item.revise` and `work_plan.revise`, because either can remove or
close native state. Annotations are hints, never authorization.

### Atomicity and partial failure

The contract deliberately combines individual semantic operations with one
bounded atomic revision:

- single-Issue content and lifecycle changes use `work_item.revise`;
- creating or opting in a plan uses `work_plan.begin`;
- membership and dependency changes are convergent set operations within
  `work_plan.revise`; and
- related plan construction, Issue creation, edges, and activation may share
  one bounded `work_plan.revise` transaction.

A plan revision is not a generic workflow language. Its closed change union,
single Project, same repository, command and creation limits, and duplicate-
target rejection make its meaning independent of input order. Forge resolves
local references, validates every precondition and invariant, and commits all
native rows, timeline events, provenance links, and the mutation receipt in one
serializable transaction. Any failed change rolls back the entire revision.
There is no partial-success array.

Notifications, webhooks, indexing, and ready-work pointers occur only after a
successful commit. They are at least once and carry stable identifiers rather
than copied descriptions. A notification failure does not roll back committed
domain state and is not reported as mutation failure. Consumers re-read the
authoritative projection on receipt.

### Durable idempotency and ambiguous outcomes

Every mutation has one durable MCP-work operation receipt. It is a protocol
safety record, not a generic audit log, Work projection, claim, or execution
record. Its uniqueness scope is the verified principal ID, canonical MCP
audience, and HMAC digest of the idempotency key. It survives access-token
refresh and is not scoped to one token instance.

The receipt stores:

- operation UUID, tool and schema version, canonical request digest, and key
  digest;
- principal ID, OAuth client registration and grant IDs, credential `jti`,
  exact scope and server-defined profile snapshot, and interface origin `mcp`;
- the registered client and installation label snapshot plus bounded
  client-reported harness, harness version when present, and model when
  supplied;
- committed `applied`, `unchanged`, or deterministic `rejected` outcome and
  timestamps; and
- stable affected Project/Issue references and local-reference resolutions.

The canonical request is the complete schema-versioned tool input after default
values are expanded, with `idempotencyKey` omitted, serialized by JSON
Canonicalization Scheme (RFC 8785). Thus object-key order and insignificant JSON
number syntax do not create a different request, while every semantic string,
reference, requested presence, and precondition does. Forge computes
`keyDigest = HMAC-SHA256(secret, "key\\0" + key)` and
`requestDigest = HMAC-SHA256(secret, "request\\0" + canonicalRequest)`. The
domain-separation prefixes and an instance secret dedicated to MCP work
receipts prevent cross-protocol or offline dictionary use. The same locator
spelling must be replayed after a repository rename; the stored stable result
references then identify the committed artifacts.

It never stores the raw token, raw idempotency key, request Markdown, complete
request body, copied Work state, prompts, model inputs or outputs, or hidden
object display data. The bounded labels are annotations, not actor credentials.
An HMAC key distinct from cursor signing keys protects key digests against
offline guessing.

Receipt detail is retained while affected artifacts exist. Deletion replaces
eligible detail with a compact key/request tombstone; the tombstone has no
automatic key-reuse expiry. Privacy policy may further minimize it only while
preserving a digest that continues to reject the same
principal/audience/key combination. This prevents a delayed retry from
duplicating a creation after artifact deletion or a TTL. A later retention
policy may change this only through an explicit compatibility and duplicate-
risk decision.

The receipt and mutation commit together. Concurrent matching requests produce
one receipt. The same key and canonical semantic request returns the recorded
outcome, original attribution, and stable references with `replayed: true`,
followed by a fresh permission-filtered projection. Changed attribution alone
does not change request equality. The same key with a different semantic
request returns `idempotency_conflict` and discloses no earlier repository or
object.

If commit returns ambiguously, Forge looks up the receipt in a new transaction.
A matching final receipt recovers the committed result. A definitely absent
receipt permits the complete bounded transaction to retry. If the lookup itself
cannot establish the outcome, Forge returns `outcome_unknown`; the client must
retry the identical request and key. Cancellation before commit rolls back.
Cancellation or disconnect after commit cannot undo the operation.

Deterministic domain rejection returns `rejected` and commits its receipt so the
same request cannot observe a different outcome after an ambiguous response.
Serialization, timeout, cancellation, and internal failures do not produce a
final receipt. Serializable conflicts retry the whole transaction at most three
times, then return a retryable conflict with no mutation.

### Optimistic concurrency

Forge protects the authority that each operation changes:

- Markdown requires the exact current Issue `ContentVersion`;
- title requires the exact current Issue title;
- a combined title and Markdown revision validates both before either changes;
- close and reopen are desired-state operations, while dependency closure rules
  remain authoritative;
- membership and dependency operations are set-oriented and need no snapshot
  version; and
- activation, return to draft, and draft deletion require the JIT `planToken`
  plus an exact expected planning state.

All graph-dependent checks run within the ADR 0003 serializable transaction.
Stale writes return `conflict` and safe reinspection guidance, not current
hidden content. Forge does not add a persistent plan version, copied readiness,
or a generic compare-and-swap layer.

### Identity, provenance, and human visibility

The principal is the verified Forge user. Authorization comes only from that
principal, the audience-bound credential and exact scope profile, and current
repository and unit permissions. The credential instance is the OAuth access
token identified by a required random `jti`; raw credentials are never stored.

An OAuth client registration is the independently revocable credential-holder
boundary. Its client and installation labels describe that registration but
are client-supplied metadata, not proof that particular software is running.
MCP `clientInfo` and an optional model label describe one operation and are
also client-reported. Forge preserves supplied values because they are useful
for debugging and archaeology, while displaying their source and never using
them for authorization, policy, dispatch, or trust.

Issue timelines and Project views link relevant native events to the receipt
and render the human-visible meaning:

```text
Performed through MCP using @principal's authority.
Authorized client: <registered client> — <installation label>.
Client reported: <harness> / <model>.
```

When no model was supplied, the final line is `Client reported: <harness>.`

The registered and runtime labels are escaped, bounded, and visually
distinguished from verified principal and grant facts. Only viewers already
authorized for the affected object can see that provenance. Internal grant,
application, token, key, and request digests are not rendered. Deleting a
registration does not rewrite an earlier receipt's bounded label snapshot.

This is useful attribution, not attestation. Forge makes no claim that a model
or harness label is true, maintains no registry of allowed values, and does not
call either value a verified actor.

### OAuth, consent, and permissions

MCP work mutations require OAuth. Personal access tokens remain exactly
read-only and never authorize mutation tools. OAuth client registrations are
public and MCP-resource-exclusive. They use the canonical MCP resource
audience, PKCE S256, header bearer credentials, short-lived access tokens, and
the existing refresh replay protections. An MCP-audience token cannot
authorize REST, and a general OAuth token cannot authorize MCP.

The server exposes a constrained programmatic MCP client bootstrap so a
harness installation can obtain and persist a client ID once. The bootstrap is
advertised through OAuth authorization metadata and accepts only a closed
public-client registration profile:

- one bounded client label and an optional bounded installation label;
- public clients with no client secret or client authentication method;
- the canonical MCP resource only;
- exact HTTPS redirects without fragment or user information, or native-app
  loopback HTTP redirects using Forge's existing dynamic-port normalization;
- PKCE S256 for every subsequent authorization;
- no requested OAuth scopes, profile, repository, principal, or authority; and
- bounded metadata size and redirect count, with no fetched logo, software
  statement, or client-supplied executable content.

The bootstrap creates a high-entropy, short-lived provisional registration.
It cannot receive a token, call MCP, or authorize another Forge interface. The
first successful browser consent binds and finalizes it for the authenticated
principal; an unapproved registration expires and is deleted. A finalized
registration cannot be transferred to or authorized by another principal.
Another principal or independently revocable installation bootstraps another
registration.

Client and installation labels, redirect URIs, and redirect class become
immutable when the registration is finalized. Changing any of them requires a
new bootstrap and consent, followed by revocation of the old grant. The bound
principal may delete a finalized registration through the Applications
settings only when it has no active grant; deletion invalidates any remaining
authorization codes. Receipts retain their bounded label snapshots. Forge does
not add a remote registration-update credential or endpoint in this slice.
An ungranted finalized registration is inert, remains visible to its bound
principal, and persists for reconnect until that principal deletes it; Forge
does not automatically reap finalized registrations.

This endpoint is not an unbounded application-creation API. It has independent
enablement, non-blocking capacity, request-size and metadata bounds,
per-source and instance-wide rate limits, and a cap on outstanding provisional
registrations. Cleanup is safe to repeat. Operators may disable new bootstrap
without invalidating established registrations and grants. Registration
metadata is treated as untrusted text throughout consent and settings views.
The provisional lifetime defaults to 30 minutes and is configurable only
within a documented 10-to-60-minute range. Expiry during login or consent
creates no grant and returns an invalid-client result that tells the harness to
bootstrap again.

A source-rotating unauthenticated caller can still fill the instance-wide
provisional cap and temporarily deny new onboarding until expiry or cleanup.
This bounded availability tradeoff is accepted so storage cannot grow without
limit; it never affects established registrations, grants, refresh, or MCP
calls. Deployment rate limiting remains an additional outer control.

The visible onboarding remains the ordinary authorization-code flow:

1. The harness silently obtains or reuses its installation client ID.
2. It opens the browser authorization request.
3. Forge authenticates the principal when needed and displays registered
   client metadata, the requested server-defined profile, and its exact scopes.
   The consent page labels the name and installation as client-provided and not
   verified by Forge. It also shows `Local application (loopback)` or the exact
   HTTPS callback origin so an attacker-chosen label is not the only client
   context.
4. The principal approves or denies once.
5. Forge finalizes the registration, creates the grant, and redirects an
   authorization code to the exact registered callback.
6. The harness exchanges the code, stores its credentials, and refreshes them
   silently until revocation or a later profile change.

Client bootstrap never grants authority. Browser consent is the only step that
creates a grant.

Forge defines two exact consent profiles. `Read` is exactly
`read:repository`. `Work Planning` is exactly:

```text
read:repository write:issue write:repository
```

The server-defined profile is derived from and governed by the grant's exact
canonical scope set; it is not permanently bound to the client application or
duplicated as a competing permission record. Scope members may arrive in any
order, but Forge canonicalizes them and rejects empty, duplicate, unknown,
additional, or non-profile sets throughout authorization, token exchange,
refresh, verification, metadata, and challenge paths.

One registration has one current grant for its principal. Moving from `Read`
to `Work Planning`, or back, is a new consent decision rather than an in-place
scope edit. Forge invalidates the old grant and every authorization code,
access-token lookup, and refresh lineage that names it; only after explicit
approval does it create a new grant and credential lineage. An old refresh
token cannot restore prior or broader authority, and an old access token cannot
inherit the replacement grant's scopes. Failed or denied consent leaves the
old grant unchanged. Forge never silently escalates a grant.

Work Planning consent describes that Forge may create, edit, close, and reopen
Issues; change plan memberships and dependencies; and create, activate, return
to draft, or delete repository plans wherever current permissions allow. It
also says it cannot push or merge code, administer repositories, or run
agents. Local logout discards the harness credentials but need not delete the
persisted client ID; reconnecting after logout requires authorization when no
valid refresh credential remains. Server-side revoke removes the grant and
invalidates its credential lineage while leaving the harmless client
registration available for a later explicit reconnect.

The principal-facing Applications page is an authority inspection surface. For
each grant it shows the registered client and optional installation label,
server-defined profile, exact scopes, authorization time, credential-rotation
time, active state, public/PKCE and redirect class, and a revoke action. It does
not claim last-use telemetry that Forge does not possess, place model labels on
the grant, or expose raw client, grant, token, or credential identifiers.

The earlier fixed ADR 0002 read application and ADR 0004 write application have
no released or supported compatibility contract. The current pre-release
dogfood database and credentials are disposable, so the
cutover removes both fixed applications and recreates the dogfood substrate
from an empty database after the replacement is complete. Forge adds no grant,
receipt, actor-trust, client-ID, token-lineage, or application-metadata
migration, backfill, alias, tombstone, or dual-read path for this transition.

Protected Resource Metadata advertises supported exact scopes, while
authorization metadata advertises the constrained client-bootstrap endpoint.
Protocol and tool discovery remain instance-scoped and permission-neutral:
enabled mutation tools may be listed to a read credential, but invocation still
requires the Work Planning profile and current permissions. Discovery never
enumerates accessible repositories. When mutation enablement is off, the Work
Planning profile and write tools are not advertised or issuable. Disabling
bootstrap stops new registrations without changing existing authorization.

Scopes only cap a credential. Every call rechecks repository state, unit state,
and native permissions:

| Operation | Required native authority |
| --- | --- |
| Inspect item | Issues read; Projects read for selected plan detail |
| Inspect plan | Projects read and Issues read |
| Create or opt in plan | Projects write; open repository; Projects enabled |
| Ensure plan membership | Issues write and Projects write |
| Create Issue in plan | existing Issue-create authority and Projects write |
| Revise title/Markdown | existing Issue authority: poster or Issues write |
| Ensure dependency | blocked Issue write, prerequisite read, dependencies enabled |
| Close or reopen | existing Issue write and dependency closure enforcement |
| Activate/draft/delete plan | Projects write and complete server validation |

Planning membership is stronger than ordinary board assignment because it can
change an active ready frontier. Ordinary disabled Projects retain existing
compatibility. New plan dependencies are same-repository; existing readable
external prerequisites remain observable and blocking as ADR 0003 requires.
Mutations are forbidden in archived repositories.

### Non-disclosure and errors

Malformed MCP messages use protocol errors. Well-formed tool calls return the
structured envelopes above. Error codes are the stable machine vocabulary:

| Code | Meaning |
| --- | --- |
| `invalid_input` | Schema or semantic input is invalid |
| `unavailable` | Top-level object is missing, private, or unreadable |
| `not_permitted` | Readable object cannot be mutated by this principal |
| `conflict` | An optimistic precondition is stale |
| `invalid_plan` | Readable plan violates a planning invariant |
| `invalid_dependency` | Edge is invalid, undisclosed, cyclic, or over bound |
| `invalid_cursor` | Cursor is invalid, expired, or bound elsewhere |
| `limit_exceeded` | A disclosed request or result exceeds a fixed bound |
| `client_attribution_required` | Required MCP harness metadata is missing or malformed, or supplied Forge model metadata is malformed |
| `busy` | Endpoint capacity is unavailable |
| `timeout` | Execution deadline elapsed before a known commit |
| `cancelled` | Caller cancelled before a known commit |
| `idempotency_conflict` | This principal reused the key for another request |
| `outcome_unknown` | Commit outcome cannot yet be established |
| `mutation_failed` | Non-retryable internal failure with no known commit |

Before Forge establishes top-level read authority, missing, private, and denied
repositories, plans, and items all return `unavailable`. After a top-level
object is readable, insufficient write authority may return `not_permitted`
without policy detail. Hidden paths, cycles through unreadable nodes, and graph
bound exhaustion all return the same `invalid_dependency` shape and make no
change. A cycle explanation is present only if every involved Issue is
readable. An idempotency conflict reveals only that the principal used the key,
not the earlier tool, repository, or object.

Nested hidden objects use undisclosed placeholders and fail closed for
readiness. Logs use the operation UUID and stable internal error class; they do
not log credentials, keys, request Markdown, hidden references, or raw database
errors.

### Limits, cancellation, and execution behavior

All MCP tools share one endpoint-wide, non-blocking in-flight budget. Adding a
tool does not multiply capacity. The existing defaults remain one MiB request
and response bodies, eight in-flight calls, and a 30-second execution deadline.
Reads default to 25 items and permit at most 100 per page. A plan revision has
at most 50 changes and 20 new Issues; titles are at most 255 characters and
Markdown at most 65,536 bytes. Domain-owned graph, projection, SQL, Git, and
output budgets add stricter finite limits documented in configuration.

Client bootstrap has a separate small request limit, capacity budget,
provisional-registration lifetime, outstanding-registration cap, and rate
limits because it is reachable before authorization. Those bounds protect the
OAuth application table without consuming a Work execution slot. They do not
become a general Forge request-rate authority.

Forge rejects predictable over-limit input before mutation. It checks context
cancellation during traversal, database work, and Git inspection. A call that
cannot enter the shared capacity budget returns `busy`; it does not queue
unbounded work. The reverse proxy remains the deployment rate-limit authority,
as ADR 0002 decided.

The tools are bounded direct operations. They do not use MCP Tasks,
elicitation, progress, or sampling. A transport cancellation before commit
rolls back; after commit it only stops response construction, and idempotent
replay recovers the result.

### ADR 0003 workflow mapping

| ADR 0003 workflow | MCP contract |
| --- | --- |
| Interpret one Issue fragment | `work_item.inspect`; no mutation or inferred plan |
| Discover and evaluate a plan | `work_plan.inspect`, then selected `work_item.inspect` |
| External adoption for delivery | Reinspect selected context; adoption remains external |
| Human/AI partial delivery handoff | Inspect delivery summary, then `pull_request.inspect` |
| Review finds planning holes | Return active plan to draft, revise, then activate atomically or in guarded steps |
| Delivery discovers new work | `work_plan.revise` creates a distinct Issue, membership, and explicit edge |
| Failed validation reopens work | `work_item.revise` desires `open`; retained delivery remains visible |
| Concurrent discovery and mutation | Stable refs, set operations, exact content preconditions, and idempotent recovery |
| Human changes the same plan | Shared work operations; next MCP read recomposes native facts |
| Ready-work handoff | Compact external pointer; MCP reinspection supplies authority |

No workflow changes Forge into an adopter, owner, dispatcher, scheduler,
harness, or runtime.

## Consequences

### Benefits

- Five semantic tools cover inspection and safe planning without generic CRUD.
- Bounded atomic plan revision prevents half-created plans while set operations
  remain naturally retryable.
- Durable receipts make creation safe under disconnects without persisting a
  second work aggregate.
- Exact OAuth profiles and native permission checks prevent scope or interface
  confusion.
- Independently revocable installation registrations avoid shared credential
  lineages without a manual application-creation ceremony.
- Humans can inspect delegated authority and see useful client-reported
  provenance without false actor claims.
- Human and MCP interfaces remain symmetric through shared domain operations.

### Costs and risks

- Cross-backend serializable retry is foundational work and must be proven on
  every supported database.
- Transactional persistence must be separated from post-commit notifications.
- A pre-authorization client-bootstrap endpoint adds application-table abuse,
  redirect-validation, expiry, cleanup, and rate-limit obligations.
- Profile replacement must invalidate old access and refresh lineages without
  silently changing their scope.
- Durable key tombstones create retention and privacy obligations.
- Permission-filtered projections and ambiguous-result fault injection require
  a substantial security test matrix.
- A JIT plan token can conflict after any relevant native change; clients must
  re-inspect rather than assume a stable plan snapshot.

## Alternatives considered

### Expose generic Project and Issue CRUD

Rejected. Clients would reconstruct ADR 0003, bypass shared invariants, and
receive more authority than the semantic workflows require.

### Expose one tool per database mutation

Rejected. Creating an Issue, adding it to a plan, connecting dependencies, and
activating the plan could partly succeed across lost responses.

### Expose a generic transactional operation array

Rejected. An open-ended operation language would be a workflow interpreter and
would recreate generic CRUD. The selected plan revision has one repository,
one Project, a closed change union, and fixed bounds.

### Require one whole-plan revision for every change

Rejected. Membership and dependency presence are sets and should converge
without rejecting independent concurrent changes. Exact preconditions remain
for content and lifecycle decisions where stale intent matters.

### Persist a Work or plan revision aggregate

Rejected. ADR 0003 permits only Project planning state as new plan-specific
state. JIT tokens and protocol receipts protect writes without copying work.

### Use one fixed MCP client per scope profile

Rejected. It collapses unrelated harness installations into one display label
and refresh lineage, makes revocation less useful, and hard-binds a client
registration to current authority. Per-installation registrations plus
grant-owned profiles preserve explicit consent without manual client setup.

### Require users to create OAuth applications manually

Rejected. Copying client IDs and redirect URIs into each harness makes first
connect unnecessarily fragile. A constrained bootstrap can create the same
durable revocation boundary without granting authority before browser consent.

### Expose unrestricted Dynamic Client Registration

Rejected. General confidential clients, arbitrary redirects, client-selected
scopes, permanent ungranted applications, and unbounded metadata create an
unnecessary application-registration and phishing surface. The selected
bootstrap accepts only the MCP public-client profile and expires unapproved
registrations.

### Trust or register harness and model identities

Rejected. It would conflate useful client-reported attribution with principal,
credential, and grant authority. Forge preserves bounded labels with an
explicit source marker and makes no attestation claim.

### Require model attribution

Rejected. Standard MCP clients and bridges do not necessarily expose a seam for
Forge-specific request metadata. Requiring it would block otherwise valid
mutations. A supplied model remains strictly validated, client-reported, and
outside authorization and idempotency semantics.

### Expire idempotency keys after a short TTL

Rejected initially. A delayed retry could duplicate a created Issue or plan.
Compact tombstones are safer until Forge has an explicit privacy-compatible
retention policy.

### Use GraphQL, MCP Tasks, or an agent registry

Rejected. None is necessary for bounded semantic work inspection and mutation,
and each would enlarge a deliberately narrow boundary.

## Acceptance criteria

An implementation conforms when:

- all five tools implement the exact closed schemas, stable references,
  annotations, bounds, and error vocabulary in this decision;
- every projection and mutation is provided by the same work and Issue domain
  operations used by human interfaces;
- no tool exposes generic Project/Issue CRUD, consumer-defined queries, claims,
  scheduling, execution, or copied Work state;
- read tools have no view or persistence side effect and use signed,
  permission-rechecked, non-snapshot cursors;
- every mutation requires the grant-owned Work Planning OAuth profile, durable
  idempotency and standard `clientInfo`; optional bounded model metadata is
  validated when supplied; PAT and Read OAuth credentials cannot invoke
  mutation tools;
- same-key replay, concurrent duplicate calls, ambiguous commit, cancellation,
  response loss, and different-payload conflict have deterministic tests;
- a bounded plan revision commits native facts, timeline events, provenance,
  and its receipt atomically or commits none of them;
- notifications and other effects occur only after commit and do not turn an
  effects failure into a false rollback report;
- Markdown, title, plan lifecycle, set-oriented, serializable graph, and stale-
  token behavior match the concurrency rules above;
- a read-after-write response contains the committed receipt and a fresh,
  permission-filtered projection without storing that projection;
- provenance distinguishes verified principal and grant facts, registered
  client metadata, client-reported harness and optional model metadata, and MCP
  origin and is human-visible without exposing credential internals;
- same-key replay returns the first receipt's attribution, while different
  attribution alone neither conflicts nor changes semantic execution;
- client bootstrap is public-client-only, MCP-exclusive, redirect constrained,
  PKCE S256-bound, bounded, rate-limited, independently disableable, and cleans
  up expired unapproved registrations;
- finalized registration metadata is immutable, deletion requires the bound
  principal and no active grant, and metadata changes require a new bootstrap
  and consent;
- onboarding requires no manual application creation and creates no authority
  before authenticated browser consent;
- consent marks registered names as client-provided and unverified and exposes
  the loopback class or exact HTTPS callback origin;
- each harness installation has an independently revocable registration and
  credential lineage, and the grant page shows its profile, exact scopes,
  authorization and rotation times, state, redirect class, and revoke action;
- profile changes atomically replace the old grant and invalidate old code,
  access, and refresh lineages without allowing silent escalation;
- clean-slate cutover removes the fixed clients and recreates the disposable
  dogfood substrate without compatibility aliases, backfills, tombstones, or
  dual-read paths;
- OAuth audience, exact scope profile, consent, bootstrap, redirect, PKCE,
  refresh, revocation, repository permission, unit permission, and REST
  isolation have negative tests;
- missing, denied, hidden-path, over-bound, stale, and idempotency errors cannot
  disclose inaccessible identities or prior requests;
- one endpoint-wide capacity limit, body/output/semantic bounds, cancellation,
  timeout, and three-attempt serializable retry are enforced;
- every ADR 0003 workflow maps to this contract without giving Forge execution
  ownership; and
- ADR 0003 remains Proposed, and neither ADR 0003 nor this dependent decision
  becomes Accepted before Proposed ADR 0001 is accepted.

## Deferred decisions

- Verified delegated actor credentials and agent identity.
- Harness or model registries, verification, attestation, and policy.
- Last-use telemetry for OAuth grants and client registrations.
- Claims, leases, adoption, and execution ownership.
- Organization, cross-repository, and portfolio plans.
- New cross-repository dependency creation.
- Structured acceptance criteria and semantic duplicate detection.
- A general event feed, broker, outbox, or projection engine.
- GraphQL and generic resource mutation.
- Expanding MCP scope beyond work planning, including review, merge, or code
  mutation.
