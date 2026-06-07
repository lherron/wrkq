The clean design is to add **prompt and scope policy to the PBC workflow as versioned template metadata**, then bind the **actual `scopeRef` at run launch time**. Do not bake a literal `scopeRef` into the PBC template unless the workflow is truly pinned to one agent/project forever.

The boundary should be:

```text
PBC template:
  declares role, phase, transition, evidence, and prompt guidance.

wrkf next:
  returns canonical next action, current state, revision, contextHash, blockers, obligations, effects.

ACP/HRC launch:
  resolves concrete scopeRef/laneRef,
  builds agent prompt from wrkf projection + PBC prompt metadata,
  launches runtime,
  binds external HRC run/session/scope back to wrkf run.
```

That matches the refactor direction: wrkf owns workflow truth, while ACP owns HRC launch, role/session convenience, and workflow participant launch prompts; ACP must not persist alternate workflow state.  [oai_citation:0‡CANONICAL_WORKFLOW_REFACTOR.md](sediment://file_000000006a50720fbf80a2b314936df0) The participant-run flow is already sketched as `task.inspect → next → run.start → build HRC launch prompt from wrkf run/task/next projection → launch HRC → run.bindExternal({ deliveryRef: { scopeRef, laneRef, generation } })`, with `sessionRef.scopeRef` and `initialPrompt` in the request shape.  [oai_citation:1‡CANONICAL_WORKFLOW_REFACTOR.md](sediment://file_000000006a50720fbf80a2b314936df0)

## Use `nextActionModel` for PBC prompt metadata

The current Go template type already has:

```go
NextActionModel map[string]json.RawMessage `json:"nextActionModel,omitempty"`
```

in `internal/workflow/types.go:29`.

That is the right place to put prompt/scoping metadata **today**, because unknown top-level JSON fields are not part of the typed template struct and will be dropped from the canonical persisted definition. `nextActionModel` is typed as raw JSON, so it survives template install/hash/persistence.

I would add a `nextActionModel` block like this:

```json
{
  "nextActionModel": {
    "schemaVersion": "wrkf.next-action-model.v1",

    "scope": {
      "required": true,
      "source": "participantRun.sessionRef.scopeRef",
      "defaultShape": "agent:<agentId>:project:<projectId>:task:<taskId>:role:<role>",
      "allowedKinds": [
        "project-task-role",
        "project-task",
        "project-role"
      ],
      "laneDefault": "pbc-refinement",
      "handoffPolicy": "same-scope-or-authorized-descendant"
    },

    "promptCatalog": {
      "pbc.agent.base.v5": {
        "kind": "inline-template",
        "digest": "sha256:<fill-after-canonicalizing>",
        "summary": "Base instructions for agent-owned PBC refinement work."
      },
      "pbc.product_owner.base.v5": {
        "kind": "inline-template",
        "digest": "sha256:<fill-after-canonicalizing>",
        "summary": "Base instructions for product-owner clarification and patch decisions."
      }
    },

    "roles": {
      "agent": {
        "basePromptRef": "pbc.agent.base.v5",
        "purpose": "Normalize one-line feedback into Behavior Note, draft compact PBC, pressure-test it, and record typed evidence.",
        "hardRules": [
          "Use wrkf next as the source of legal next actions.",
          "Do not apply a transition unless its evidence and obligations are satisfied.",
          "Record evidence before applying the transition that depends on it.",
          "When blocked on product_owner, ask exactly one highest-leverage question or patch decision.",
          "Do not invent product-owner answers."
        ]
      },
      "product_owner": {
        "basePromptRef": "pbc.product_owner.base.v5",
        "purpose": "Answer clarification or patch decision obligations with minimal decisive input.",
        "hardRules": [
          "Answer only the blocking decision requested by the workflow.",
          "Prefer a crisp route or answer over broad redesign.",
          "Do not mutate workflow state directly; provide evidence or obligation response."
        ]
      }
    },

    "phaseGuidance": {
      "open/intake": {
        "agentInstruction": "Extract the raw feedback, normalize the signal, infer likely type, and identify the candidate highest-leverage clarification question.",
        "expectedEvidence": ["intake_metadata"],
        "avoid": ["writing a full PBC before the feedback is normalized"]
      },
      "active/behavior_note": {
        "agentInstruction": "Write or verify the Behavior Note, then decide whether one clarification is needed before drafting.",
        "expectedEvidence": ["behavior_note", "pre_interview_analysis"],
        "decisionFacts": {
          "pre_interview_analysis.clarification_needed": [true, false]
        }
      },
      "waiting/clarification": {
        "agentInstruction": "Do not continue drafting. Wait for product_owner clarification_response or an authorized waiver.",
        "expectedEvidence": ["clarification_response"],
        "blockedBy": ["clarification_response"]
      },
      "active/pbc_draft": {
        "agentInstruction": "Draft a compact PBC from current evidence and prepare it for pressure review.",
        "expectedEvidence": ["pbc_draft"]
      },
      "active/pressure": {
        "agentInstruction": "Run the pressure pass. Produce exactly one verdict: ready, needs_patch, or too_vague. Include tighten list and patch when applicable.",
        "expectedEvidence": ["pressure_pass", "pbc_final when verdict is ready"]
      },
      "waiting/patch_decision": {
        "agentInstruction": "Do not revise or finalize until product_owner records patch_decision.route as finalize or revise.",
        "expectedEvidence": ["patch_decision"],
        "blockedBy": ["patch_decision"]
      }
    },

    "transitionGuidance": {
      "normalize_feedback": {
        "prompt": "Normalize raw feedback into intake_metadata. Capture original wording, normalized signal, likely type, uncertainty, and candidate clarification question.",
        "produceEvidence": ["intake_metadata"],
        "then": "Apply normalize_feedback if revision/context are current."
      },
      "ask_clarification": {
        "prompt": "If clarification_needed is true, ask exactly one highest-leverage question. Make the question answerable in one short response.",
        "produceEvidence": ["pre_interview_analysis"],
        "then": "Apply ask_clarification to open the product_owner obligation."
      },
      "draft_pbc": {
        "prompt": "Draft the smallest useful PBC. Include behavior, preconditions, boundaries, acceptance signal, and known uncertainty. Do not expand into implementation design.",
        "produceEvidence": ["pbc_draft"],
        "then": "Apply draft_pbc."
      },
      "run_pressure_pass": {
        "prompt": "Pressure-test the draft PBC against ambiguity, missing preconditions, overbreadth, unverifiable acceptance, and implementation leakage.",
        "produceEvidence": ["pressure_pass"],
        "then": "Apply run_pressure_pass."
      },
      "finalize_ready_pbc": {
        "prompt": "Only finalize if pressure_pass.verdict is ready and the pbc_final artifact is derived from the current draft.",
        "produceEvidence": ["pbc_final"],
        "then": "Apply finalize_ready_pbc."
      },
      "request_patch_decision": {
        "prompt": "Summarize the pressure patch and ask product_owner whether to finalize with patch or revise.",
        "produceEvidence": ["pressure_pass"],
        "then": "Apply request_patch_decision."
      },
      "revise_too_vague_pbc": {
        "prompt": "Revise the PBC using the pressure pass. Preserve the behavioral contract; remove ambiguity before re-running pressure.",
        "produceEvidence": ["pbc_draft"],
        "then": "Apply revise_too_vague_pbc."
      },
      "finalize_after_patch_decision": {
        "prompt": "Finalize only if patch_decision.route is finalize and the obligation was satisfied by an authorized product_owner.",
        "produceEvidence": ["pbc_final"],
        "then": "Apply finalize_after_patch_decision."
      },
      "revise_after_patch_decision": {
        "prompt": "Revise the draft according to patch_decision.route=revise. Cite the product-owner decision in the new draft evidence.",
        "produceEvidence": ["pbc_draft"],
        "then": "Apply revise_after_patch_decision."
      }
    }
  }
}
```

Avoid keys named `command`, `cmd`, `argv`, `shell`, `cwd`, or `env` inside the template. The current validator rejects templates containing inline executable command keys anywhere in the canonical JSON (`internal/workflow/service.go:210-237`). Use fields like `then`, `allowedOps`, `operatorHint`, or `cliHint` instead of `command`.

## Use `responsibility` on transitions for scope/lane routing

The current transition model already supports:

```go
Responsibility *ResponsibilitySpec `json:"responsibility,omitempty"`
```

with:

```go
Role  string `json:"role,omitempty"`
Scope string `json:"scope,omitempty"`
Lane  string `json:"lane,omitempty"`
```

in `internal/workflow/types.go:57-78`.

So for PBC, add `responsibility` to each transition. For example:

```json
{
  "id": "draft_pbc",
  "description": "Pre-interview analysis found no clarification is needed; draft the compact PBC.",
  "from": {
    "status": "active",
    "phase": "behavior_note"
  },
  "by": ["agent"],
  "responsibility": {
    "role": "agent",
    "scope": "task",
    "lane": "pbc-refinement"
  },
  "requires": [
    {
      "evidence": {
        "kind": "behavior_note"
      }
    },
    {
      "evidence": {
        "kind": "pre_interview_analysis",
        "facts": {
          "clarification_needed": false
        }
      }
    }
  ],
  "outcomes": [
    {
      "id": "drafted",
      "description": "Draft PBC is ready for pressure pass.",
      "when": {
        "always": true
      },
      "to": {
        "status": "active",
        "phase": "pbc_draft"
      }
    }
  ]
}
```

That makes `wrkf next` able to say: “the next action belongs to role `agent`, in the task scope, on lane `pbc-refinement`.” ACP/HRC then resolves the concrete runtime scope.

## Resolve concrete `scopeRef` at run launch

Do **not** put this in the template:

```json
"scopeRef": "agent:cody:project:wrkq:task:T-123:role:agent"
```

That is run/session-specific.

Instead, the launch request should carry it:

```ts
POST /v1/workflow-participant-runs

{
  "taskId": "T-123",
  "role": "agent",
  "actor": {
    "kind": "agent",
    "id": "cody"
  },
  "idempotencyKey": "pbc:T-123:agent:rev-4",
  "sessionRef": {
    "scopeRef": "agent:cody:project:wrkq:task:T-123:role:agent",
    "laneRef": "pbc-refinement"
  },
  "launchRuntime": true
}
```

The existing scope grammar supports the long form:

```text
agent:<agentId>[:project:<projectId>[:task:<taskId>][:role:<roleName>]]
```

from `internal/scope/types.go:5-10`, and validation accepts `agent`, `project`, `project-role`, `project-task`, and `project-task-role` shapes (`internal/scope/scope_ref.go:25-98`).

Then bind the launched HRC run back into wrkf:

```ts
wrkf.run.bindExternal({
  runId: wrkfRun.id,
  externalRunRef: launched.runId,
  deliveryRef: {
    kind: 'hrc',
    hostSessionId,
    runtimeId,
    launchId,
    scopeRef: 'agent:cody:project:wrkq:task:T-123:role:agent',
    laneRef: 'pbc-refinement',
    generation
  },
  idempotencyKey: `${idempotencyKey}:bindExternal`
})
```

The refactor doc explicitly wants `run.bindExternal` to hold HRC run/session/runtime/scope/lane/generation references, with ACP using its own run store only as execution telemetry/fencing, not workflow truth.  [oai_citation:2‡CANONICAL_WORKFLOW_REFACTOR.md](sediment://file_000000006a50720fbf80a2b314936df0)

## Prompt construction should be a compiler step, not template execution

The participant launch service should compile a prompt from these inputs:

```ts
type PbcPromptBuildInput = {
  task: WrkfTaskProjection
  instance: WrkfInstanceProjection
  next: WrkfNextActionResponse
  run: WrkfRun
  scopeRef: string
  laneRef?: string
  templateNextActionModel: PbcNextActionModel
  evidenceSummary: EvidenceSummary[]
  obligations: ObligationSummary[]
  pendingEffects: EffectSummary[]
}
```

The output should be the initial HRC prompt:

```text
You are operating inside a wrkf-owned PBC workflow.

Scope:
- scopeRef: agent:cody:project:wrkq:task:T-123:role:agent
- laneRef: pbc-refinement
- role: agent
- actor: cody

Canonical workflow:
- workflow: pbc-progressive-refinement@5
- task: T-123
- instance: wrkf_inst_...
- state: active/behavior_note
- revision: 2
- contextHash: sha256:...

Current next action:
- action id: collect_pre_interview_analysis
- kind: collect_evidence
- unblocks: draft_pbc or ask_clarification
- why: transition needs pre_interview_analysis evidence

PBC-specific instruction:
Write pre_interview_analysis. Decide whether clarification is needed.
If clarification is needed, produce:
  facts.clarification_needed = true
  one highest-leverage question
If not needed, produce:
  facts.clarification_needed = false
  concise rationale sufficient to draft the PBC

Hard rules:
- Treat wrkf next as the legal-action source.
- Add evidence before applying transitions that depend on it.
- Do not invent product-owner answers.
- Do not apply a transition with stale revision/context.
- Do not modify workflow state outside wrkf.
```

That prompt is execution context, not canonical workflow truth. The canonical facts remain in wrkf evidence, obligations, transitions, runs, and effects.

## Add prompt hints to `NextAction`, but keep them non-authoritative

Current `NextAction` already has `Why`, `Unblocks`, `BlocksOn`, `Preflight`, `ExpectedState`, and `Guardrails` (`internal/workflow/types.go:381-395`). I would extend it with prompt-hint fields rather than a giant full prompt:

```go
type NextAction struct {
    // existing fields...
    PromptRef     string                 `json:"promptRef,omitempty"`
    PromptHints   map[string]interface{} `json:"promptHints,omitempty"`
    ScopeHint     *ScopeHint             `json:"scopeHint,omitempty"`
}

type ScopeHint struct {
    Required     bool     `json:"required"`
    Kind         string   `json:"kind,omitempty"`       // project-task-role
    Role         string   `json:"role,omitempty"`       // agent
    Lane         string   `json:"lane,omitempty"`       // pbc-refinement
    Source       string   `json:"source,omitempty"`     // sessionRef.scopeRef
    AncestorsOK  []string `json:"ancestorsOk,omitempty"`
}
```

Then `Service.Next` can merge the template’s `nextActionModel` into each generated action. ACP can build the final prompt, but it should not decide legality. That is consistent with the refactor’s warning to remove ACP participant/supervisor context routes and replace them with launch prompt construction from wrkf task/run/next projections.  [oai_citation:3‡CANONICAL_WORKFLOW_REFACTOR.md](sediment://file_000000006a50720fbf80a2b314936df0)

## Recommended implementation sequence

Start with the smallest viable change:

1. Add `responsibility` to every PBC transition: role, scope, lane.
2. Add `nextActionModel.schemaVersion`, `scope`, `roles`, `phaseGuidance`, and `transitionGuidance` to `pbc/workflow-template.json`.
3. Teach `wrkf next` to include `PromptRef`, `PromptHints`, and `ScopeHint` derived from the template metadata.
4. Build `packages/acp-server/src/wrkf/launch-context.ts` or equivalent to compile the HRC launch prompt from `task.inspect`, `next`, `run.start`, and template metadata.
5. Accept `sessionRef.scopeRef` on participant-run launch and validate it against the canonical scope grammar.
6. Store the actual HRC binding through `wrkf.run.bindExternal`, including `scopeRef`, `laneRef`, and `generation`.
7. Keep the full generated prompt out of durable workflow truth unless you need audit replay; if you do store it, store it as run execution metadata or a content-addressed prompt artifact, not as evidence needed to transition.

## For PBC specifically

The agent prompt should always answer four questions:

```text
1. What phase am I in?
2. What is the legal next action?
3. What evidence or obligation must I produce/satisfy?
4. What must I not do from this phase?
```

For example, at `active/pressure`, the agent should see:

```text
Phase: active/pressure
Role: agent
Scope: agent:cody:project:wrkq:task:T-123:role:agent
Legal work:
- Produce pressure_pass evidence.
- verdict must be one of: ready, needs_patch, too_vague.
- If ready, produce pbc_final before finalize_ready_pbc.
- If needs_patch, request product_owner patch_decision.
- If too_vague, revise the draft.

Do not:
- finalize without pressure_pass.verdict=ready;
- use a pressure pass from an old draft hash;
- ask multiple broad product-owner questions;
- mutate workflow state outside wrkf transition.apply.
```

That is the operational sweet spot: the workflow template carries stable, versioned guidance; wrkf `next` carries the current legal state/action; ACP/HRC supplies the concrete `scopeRef` and runtime launch; and the agent receives a prompt that is specific enough to act without letting ACP become a shadow workflow engine.
