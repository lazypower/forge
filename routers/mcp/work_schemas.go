// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"gitea.dev/modules/json"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	workPlanBeginToolName  = "work_plan.begin"
	workItemReviseToolName = "work_item.revise"
	workPlanReviseToolName = "work_plan.revise"
)

const workInputDefinitions = `
  "repository":{"type":"object","additionalProperties":false,"required":["owner","name"],"properties":{"owner":{"type":"string","minLength":1,"maxLength":255},"name":{"type":"string","minLength":1,"maxLength":255}}},
  "page":{"type":"object","additionalProperties":false,"properties":{"limit":{"type":"integer","minimum":1,"maximum":100,"default":25},"cursor":{"type":"string","minLength":1,"maxLength":2048}}},
  "issueRef":{"type":"string","pattern":"^issue/[1-9][0-9]*$"},
  "projectRef":{"type":"string","pattern":"^project/[1-9][0-9]*$"},
  "contextRef":{"type":"string","pattern":"^project/[1-9][0-9]*/issue/[1-9][0-9]*$"},
  "idempotencyKey":{"type":"string","pattern":"^[!-~]{16,128}$"}`

const workResultDefinitions = `
  "issueRef":{"type":"string","pattern":"^issue/[1-9][0-9]*$"},
  "projectRef":{"type":"string","pattern":"^project/[1-9][0-9]*$"},
  "contextRef":{"type":"string","pattern":"^project/[1-9][0-9]*/issue/[1-9][0-9]*$"},
  "resultStatus":{"type":"string","enum":["available","unavailable","error"]},
  "repositoryResult":{"type":"object","additionalProperties":false,"required":["owner","name","url"],"properties":{"owner":{"type":"string"},"name":{"type":"string"},"url":{"type":"string","format":"uri"}}},
  "availability":{"type":"string","enum":["available","undisclosed"]},
  "referenceRef":{"oneOf":[{"$ref":"#/$defs/issueRef"},{"$ref":"#/$defs/projectRef"},{"$ref":"#/$defs/contextRef"},{"type":"string","pattern":"^pull/[1-9][0-9]*$"}]},
  "referenceSummary":{"type":"object","additionalProperties":false,"required":["availability"],"properties":{"availability":{"$ref":"#/$defs/availability"},"repository":{"$ref":"#/$defs/repositoryResult"},"ref":{"$ref":"#/$defs/referenceRef"},"url":{"type":"string","format":"uri"},"label":{"type":"string"},"state":{"enum":["open","closed","merged","disabled","draft","active","planned","ready","blocked","complete","excluded"]}},"allOf":[{"if":{"properties":{"availability":{"const":"available"}}},"then":{"required":["repository","ref","url","label","state"]}},{"if":{"properties":{"availability":{"const":"undisclosed"}}},"then":{"not":{"anyOf":[{"required":["ref"]},{"required":["repository"]},{"required":["url"]},{"required":["label"]},{"required":["state"]}]}}}]},
  "integrity":{"type":"object","additionalProperties":false,"required":["status","concerns"],"properties":{"status":{"enum":["valid","concern","incomplete"]},"concerns":{"type":"array","maxItems":100,"items":{"type":"object","additionalProperties":false,"required":["code","message"],"properties":{"code":{"type":"string"},"message":{"type":"string"}}}}}},
  "deliverySummary":{"type":"object","additionalProperties":false,"required":["repository","ref","url","state","revision","checkState"],"properties":{"repository":{"$ref":"#/$defs/repositoryResult"},"ref":{"type":"string","pattern":"^pull/[1-9][0-9]*$"},"url":{"type":"string","format":"uri"},"state":{"enum":["open","closed","merged"]},"revision":{"type":"string","pattern":"^[0-9a-f]{40,64}$"},"checkState":{"enum":["success","failure","pending","unverified","none"]}}},
  "contextSummary":{"type":"object","additionalProperties":false,"required":["ref","workPlan","derivedState","integrityStatus"],"properties":{"ref":{"$ref":"#/$defs/contextRef"},"workPlan":{"$ref":"#/$defs/projectRef"},"derivedState":{"enum":["planned","ready","blocked","complete"]},"integrityStatus":{"enum":["valid","concern","incomplete"]}}},
  "workItemResult":{"type":"object","additionalProperties":false,"required":["ref","url","title","markdown","contentVersion","state","classification","contextSummaries","projectMemberships","prerequisiteSummaries","dependentSummaries","deliverySummaries"],"properties":{"ref":{"$ref":"#/$defs/issueRef"},"url":{"type":"string","format":"uri"},"title":{"type":"string"},"markdown":{"type":"string"},"contentVersion":{"type":"integer","minimum":0},"state":{"enum":["open","closed"]},"classification":{"enum":["unplanned","planned"]},"contextSummaries":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/contextSummary"}},"projectMemberships":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/referenceSummary"}},"prerequisiteSummaries":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/referenceSummary"}},"dependentSummaries":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/referenceSummary"}},"deliverySummaries":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/deliverySummary"}}}},
  "planContextResult":{"type":"object","additionalProperties":false,"required":["ref","workPlan","workItem","derivedState","integrity","prerequisiteSummaries","deliverySummaries"],"properties":{"ref":{"$ref":"#/$defs/contextRef"},"workPlan":{"$ref":"#/$defs/projectRef"},"workItem":{"$ref":"#/$defs/issueRef"},"derivedState":{"enum":["planned","ready","blocked","complete"]},"integrity":{"$ref":"#/$defs/integrity"},"prerequisiteSummaries":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/referenceSummary"}},"deliverySummaries":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/deliverySummary"}}}},
  "workPlanResult":{"type":"object","additionalProperties":false,"required":["ref","url","title","markdown","planningState","projectState","integrity","itemSummaries","edgeSummaries","readyFrontier","excludedMembers","planToken"],"properties":{"ref":{"$ref":"#/$defs/projectRef"},"url":{"type":"string","format":"uri"},"title":{"type":"string"},"markdown":{"type":"string"},"planningState":{"enum":["draft","active"]},"projectState":{"enum":["open","closed"]},"integrity":{"$ref":"#/$defs/integrity"},"itemSummaries":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/contextSummary"}},"edgeSummaries":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/edgeSummary"}},"readyFrontier":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/contextSummary"}},"excludedMembers":{"type":"array","maxItems":100,"items":{"$ref":"#/$defs/referenceSummary"}},"planToken":{"type":"string","minLength":1,"maxLength":2048}}},
  "edgeSummary":{"type":"object","additionalProperties":false,"required":["blocked","prerequisite"],"properties":{"blocked":{"$ref":"#/$defs/referenceSummary"},"prerequisite":{"$ref":"#/$defs/referenceSummary"}}},
  "pageResult":{"type":"object","additionalProperties":false,"required":["kind","items","snapshotConsistency","reinspectBeforeAction"],"properties":{"kind":{"enum":["prerequisites","dependents","memberships","contexts","deliveries","items","edges","ready_frontier","excluded_members"]},"items":{"type":"array","maxItems":100,"items":{"oneOf":[{"$ref":"#/$defs/referenceSummary"},{"$ref":"#/$defs/contextSummary"},{"$ref":"#/$defs/deliverySummary"},{"$ref":"#/$defs/edgeSummary"}]}},"nextCursor":{"type":"string","maxLength":2048},"snapshotConsistency":{"const":"none"},"reinspectBeforeAction":{"const":true}},"allOf":[{"if":{"properties":{"kind":{"enum":["prerequisites","dependents","memberships","excluded_members"]}}},"then":{"properties":{"items":{"items":{"$ref":"#/$defs/referenceSummary"}}}}},{"if":{"properties":{"kind":{"enum":["contexts","items","ready_frontier"]}}},"then":{"properties":{"items":{"items":{"$ref":"#/$defs/contextSummary"}}}}},{"if":{"properties":{"kind":{"const":"deliveries"}}},"then":{"properties":{"items":{"items":{"$ref":"#/$defs/deliverySummary"}}}}},{"if":{"properties":{"kind":{"const":"edges"}}},"then":{"properties":{"items":{"items":{"$ref":"#/$defs/edgeSummary"}}}}}]},
  "problem":{"type":"object","additionalProperties":false,"required":["code","message","retryable"],"properties":{"code":{"type":"string"},"message":{"type":"string"},"retryable":{"type":"boolean"},"retryAfterMilliseconds":{"type":"integer","minimum":1}}}`

const workItemInspectInputSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$defs":{` + workInputDefinitions + `},
  "type":"object","additionalProperties":false,"required":["repository","workItem"],
  "properties":{"repository":{"$ref":"#/$defs/repository"},"workItem":{"$ref":"#/$defs/issueRef"},"selectedPlan":{"$ref":"#/$defs/projectRef"},"pageKind":{"type":"string","enum":["prerequisites","dependents","memberships","contexts","deliveries"],"default":"contexts"},"page":{"$ref":"#/$defs/page"}}
}`

const workPlanInspectInputSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$defs":{` + workInputDefinitions + `},
  "type":"object","additionalProperties":false,"required":["repository","workPlan"],
  "properties":{"repository":{"$ref":"#/$defs/repository"},"workPlan":{"$ref":"#/$defs/projectRef"},"pageKind":{"type":"string","enum":["items","edges","ready_frontier","excluded_members"],"default":"items"},"page":{"$ref":"#/$defs/page"}}
}`

const workReadOutputSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$defs":{` + workResultDefinitions + `},
  "type":"object","additionalProperties":false,"required":["schemaVersion","status"],
  "properties":{"schemaVersion":{"const":"1"},"status":{"$ref":"#/$defs/resultStatus"},"repository":{"$ref":"#/$defs/repositoryResult"},"workItem":{"$ref":"#/$defs/workItemResult"},"workPlan":{"$ref":"#/$defs/workPlanResult"},"selectedContext":{"$ref":"#/$defs/planContextResult"},"page":{"$ref":"#/$defs/pageResult"},"problem":{"$ref":"#/$defs/problem"}},
  "allOf":[
    {"if":{"properties":{"status":{"const":"available"}}},"then":{"required":["repository","page"],"oneOf":[{"required":["workItem"],"not":{"required":["workPlan"]}},{"required":["workPlan"],"not":{"anyOf":[{"required":["workItem"]},{"required":["selectedContext"]}]}}],"not":{"required":["problem"]}}},
    {"if":{"properties":{"status":{"const":"unavailable"}}},"then":{"not":{"anyOf":[{"required":["repository"]},{"required":["workItem"]},{"required":["workPlan"]},{"required":["selectedContext"]},{"required":["page"]},{"required":["problem"]}]}}},
    {"if":{"properties":{"status":{"const":"error"}}},"then":{"required":["problem"],"not":{"anyOf":[{"required":["repository"]},{"required":["workItem"]},{"required":["workPlan"]},{"required":["selectedContext"]},{"required":["page"]}]}}}
  ]
}`

const workPlanBeginInputSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema","$defs":{` + workInputDefinitions + `},
  "type":"object","additionalProperties":false,"required":["repository","idempotencyKey","begin"],
  "properties":{"repository":{"$ref":"#/$defs/repository"},"idempotencyKey":{"$ref":"#/$defs/idempotencyKey"},"begin":{"oneOf":[{"type":"object","additionalProperties":false,"required":["kind","title"],"properties":{"kind":{"const":"new"},"title":{"type":"string","minLength":1,"maxLength":255},"markdown":{"type":"string","maxLength":65536}}},{"type":"object","additionalProperties":false,"required":["kind","workPlan"],"properties":{"kind":{"const":"existing"},"workPlan":{"$ref":"#/$defs/projectRef"}}}]}}
}`

const workItemReviseInputSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema","$defs":{` + workInputDefinitions + `},
  "type":"object","additionalProperties":false,"required":["repository","workItem","idempotencyKey"],
  "properties":{"repository":{"$ref":"#/$defs/repository"},"workItem":{"$ref":"#/$defs/issueRef"},"idempotencyKey":{"$ref":"#/$defs/idempotencyKey"},"title":{"type":"object","additionalProperties":false,"required":["expected","desired"],"properties":{"expected":{"type":"string","maxLength":255},"desired":{"type":"string","minLength":1,"maxLength":255}}},"markdown":{"type":"object","additionalProperties":false,"required":["expectedContentVersion","desired"],"properties":{"expectedContentVersion":{"type":"integer","minimum":0},"desired":{"type":"string","maxLength":65536}}},"state":{"type":"object","additionalProperties":false,"required":["desired"],"properties":{"desired":{"type":"string","enum":["open","closed"]}}}},
  "anyOf":[{"required":["title"]},{"required":["markdown"]},{"required":["state"]}]
}`

const workPlanReviseInputSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$defs":{` + workInputDefinitions + `,
    "itemSelector":{"oneOf":[{"$ref":"#/$defs/issueRef"},{"type":"string","pattern":"^local/[A-Za-z][A-Za-z0-9_-]{0,63}$"}]},
    "planChange":{"oneOf":[
      {"type":"object","additionalProperties":false,"required":["kind","workItem","presence"],"properties":{"kind":{"const":"ensure_member"},"workItem":{"$ref":"#/$defs/itemSelector"},"presence":{"enum":["present","absent"]}}},
      {"type":"object","additionalProperties":false,"required":["kind","localReference","title"],"properties":{"kind":{"const":"create_member"},"localReference":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_-]{0,63}$"},"title":{"type":"string","minLength":1,"maxLength":255},"markdown":{"type":"string","maxLength":65536}}},
      {"type":"object","additionalProperties":false,"required":["kind","blocked","prerequisite","presence"],"properties":{"kind":{"const":"ensure_dependency"},"blocked":{"$ref":"#/$defs/itemSelector"},"prerequisite":{"$ref":"#/$defs/itemSelector"},"presence":{"enum":["present","absent"]}}},
      {"type":"object","additionalProperties":false,"required":["kind","expected","desired"],"properties":{"kind":{"const":"set_planning_state"},"expected":{"enum":["draft","active"]},"desired":{"enum":["draft","active"]}}},
      {"type":"object","additionalProperties":false,"required":["kind"],"properties":{"kind":{"const":"delete_draft"}}}
    ]}
  },
  "type":"object","additionalProperties":false,"required":["repository","workPlan","idempotencyKey","changes"],
  "properties":{"repository":{"$ref":"#/$defs/repository"},"workPlan":{"$ref":"#/$defs/projectRef"},"idempotencyKey":{"$ref":"#/$defs/idempotencyKey"},"expectedPlanToken":{"type":"string","minLength":1,"maxLength":2048},"changes":{"type":"array","minItems":1,"maxItems":50,"items":{"$ref":"#/$defs/planChange"}}}
}`

const workMutationOutputSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema","$defs":{` + workResultDefinitions + `},
  "type":"object","additionalProperties":false,"required":["schemaVersion","status"],
  "properties":{"schemaVersion":{"const":"1"},"status":{"enum":["applied","unchanged","rejected","error"]},"operation":{"type":"object","additionalProperties":false,"required":["id","replayed","changed","committedAt"],"properties":{"id":{"type":"string","format":"uuid"},"replayed":{"type":"boolean"},"changed":{"type":"boolean"},"committedAt":{"type":"string","format":"date-time"}}},"createdReferences":{"type":"object","additionalProperties":{"$ref":"#/$defs/issueRef"}},"workItem":{"$ref":"#/$defs/workItemResult"},"workPlan":{"$ref":"#/$defs/workPlanResult"},"selectedContext":{"$ref":"#/$defs/planContextResult"},"currentResultStatus":{"enum":["available","unavailable","projection_unavailable"]},"problem":{"$ref":"#/$defs/problem"}},
  "allOf":[
    {"if":{"properties":{"status":{"enum":["applied","unchanged"]}}},"then":{"required":["operation","currentResultStatus"],"not":{"required":["problem"]}}},
    {"if":{"properties":{"status":{"const":"rejected"}}},"then":{"required":["operation","problem"]}},
    {"if":{"properties":{"status":{"const":"error"}}},"then":{"required":["problem"],"not":{"anyOf":[{"required":["operation"]},{"required":["createdReferences"]},{"required":["workItem"]},{"required":["workPlan"]},{"required":["selectedContext"]},{"required":["currentResultStatus"]}]}}}
  ]
}`

func declaredWorkToolContracts() map[string]*mcpsdk.Tool {
	closedWorld := false
	return map[string]*mcpsdk.Tool{
		workItemInspectToolName: {
			Name: workItemInspectToolName, Description: "Inspect one Issue-centered WorkItem using Forge's bounded, read-only Work operation.",
			InputSchema: mustWorkSchema(workItemInspectInputSchema), OutputSchema: mustWorkSchema(workReadOutputSchema),
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		},
		workPlanInspectToolName: {
			Name: workPlanInspectToolName, Description: "Inspect one repository WorkPlan page using Forge's bounded, read-only Work operation.",
			InputSchema: mustWorkSchema(workPlanInspectInputSchema), OutputSchema: mustWorkSchema(workReadOutputSchema),
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		},
		workPlanBeginToolName: {
			Name: workPlanBeginToolName, Description: "Begin one draft repository WorkPlan.",
			InputSchema: mustWorkSchema(workPlanBeginInputSchema), OutputSchema: mustWorkSchema(workMutationOutputSchema),
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: &closedWorld},
		},
		workItemReviseToolName: {
			Name: workItemReviseToolName, Description: "Conditionally revise one Issue-centered WorkItem.",
			InputSchema: mustWorkSchema(workItemReviseInputSchema), OutputSchema: mustWorkSchema(workMutationOutputSchema),
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: &closedWorld},
		},
		workPlanReviseToolName: {
			Name: workPlanReviseToolName, Description: "Atomically apply one bounded repository WorkPlan revision.",
			InputSchema: mustWorkSchema(workPlanReviseInputSchema), OutputSchema: mustWorkSchema(workMutationOutputSchema),
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: &closedWorld},
		},
	}
}

func mustWorkSchema(source string) map[string]any {
	var schema map[string]any
	if err := json.Unmarshal([]byte(source), &schema); err != nil {
		panic(err)
	}
	return schema
}
