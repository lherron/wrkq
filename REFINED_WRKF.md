# Refined wrkf Structured Evidence Proposal

## Purpose

Add structured evidence to wrkf without turning workflows into a schema-management system.

The core primitive is:

```text
evidence.data   = rich freeform payload for humans and agents
evidence.facts  = small validated decision surface for wrkf routing
```

wrkf should reason over `facts`. It should not validate entire product artifacts such as Behavior Notes, PBCs, tickets, test plans, or runbooks.

## Current Source Context

The current wrkf model is intentionally minimal.

In `internal/workflow/types.go`, evidence kinds only carry a description:

```go
type KindSpec struct {
    Description string `json:"description,omitempty"`
}
```

Transition requirements can require evidence by kind only:

```go
type RequirementSpec struct {
    Evidence *struct {
        Kind string `json:"kind"`
    } `json:"evidence,omitempty"`
    Obligation *struct {
        Kind   string `json:"kind,omitempty"`
        ID     string `json:"id,omitempty"`
        Status string `json:"status,omitempty"`
    } `json:"obligation,omitempty"`
}
```

Predicates mirror the same limitation:

```go
type EvidencePredicate struct {
    Kind string `json:"kind"`
}
```

The database already has facts for check runs, but not for evidence:

```sql
CREATE TABLE workflow_check_runs (
  ...
  facts_json TEXT,
  ...
);

CREATE TABLE workflow_evidence (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  ref TEXT NOT NULL,
  summary TEXT,
  data_json TEXT,
  source_json TEXT NOT NULL,
  actor TEXT,
  role TEXT,
  run_id TEXT REFERENCES workflow_runs(id),
  task_etag_at_production TEXT,
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
```

`Service.AddEvidence` validates only that `data` is valid JSON, then inserts the evidence. It does not load the workflow template, validate evidence kind contracts, or validate payload shape.

`transitionBlockers` currently treats evidence requirements as presence-only:

```go
for _, e := range ev {
    if e.Kind == req.Evidence.Kind {
        found = true
        break
    }
}
```

This proposal extends the current seam rather than replacing it.

## Design Principles

- Facts are for workflow routing; data is for context.
- Facts are explicit, not hidden inside `data.facts`.
- Facts contracts live in the installed workflow template version.
- There is no global schema registry.
- There are no external schema files in v1.
- Existing templates and evidence records remain valid.
- Full JSON Schema is intentionally out of scope.
- Contracts validate small flat decision facts, not full artifacts.

## Terminology

There will be three facts concepts in the codebase:

- Task facts: `task.meta.workflowFacts`, used by task-level predicates/checks.
- Check facts: `workflow_check_runs.facts_json`, produced by hook/check execution.
- Evidence facts: `workflow_evidence.facts_json`, attached to recorded evidence and used for routing.

Docs and code should avoid using an unqualified `facts` when the distinction matters.

## Data Model

Add a new migration, for example:

```text
internal/db/migrations/000019_workflow_evidence_facts.sql
```

Minimum migration:

```sql
ALTER TABLE workflow_evidence ADD COLUMN facts_json TEXT;
```

Preferred migration if the deployed SQLite build reliably supports JSON functions:

```sql
ALTER TABLE workflow_evidence
ADD COLUMN facts_json TEXT
CHECK (
  facts_json IS NULL OR
  (json_valid(facts_json) AND json_type(facts_json) = 'object')
);
```

App-level validation is still required. The DB check is only corruption resistance.

Also update any schema dump/baseline artifacts that represent the current schema, including `schema_dump.sql` if present in the repo.

Update `workflow.Evidence`:

```go
type Evidence struct {
    ID                   string          `json:"id"`
    InstanceID           string          `json:"instanceId,omitempty"`
    Kind                 string          `json:"kind"`
    Ref                  string          `json:"ref"`
    Summary              string          `json:"summary,omitempty"`
    Facts                json.RawMessage `json:"facts,omitempty"`
    Data                 json.RawMessage `json:"data,omitempty"`
    Source               json.RawMessage `json:"source,omitempty"`
    Actor                string          `json:"actor,omitempty"`
    Role                 string          `json:"role,omitempty"`
    RunID                string          `json:"runId,omitempty"`
    TaskEtagAtProduction string          `json:"taskEtagAtProduction,omitempty"`
    ProducedAt           string          `json:"producedAt"`
}
```

Update every evidence read/write path, not just one list command:

- `Service.AddEvidence`
- `Service.ListEvidence`
- `Service.ShowEvidence`
- `listEvidenceTx`
- `listEvidenceForInstance`
- hook input construction
- effect delivery input construction
- computed obligations that read evidence
- context hash construction
- snapshot/export/import paths if they include workflow evidence

The goal is that any code path that sees evidence sees the same facts.

## Template Model

Replace anonymous requirement structs with named structs before extending them.

```go
type RequirementSpec struct {
    Evidence   *EvidenceRequirementSpec   `json:"evidence,omitempty"`
    Obligation *ObligationRequirementSpec `json:"obligation,omitempty"`
}

type EvidenceRequirementSpec struct {
    Kind  string                     `json:"kind"`
    Facts map[string]json.RawMessage `json:"facts,omitempty"`
}

type ObligationRequirementSpec struct {
    Kind   string `json:"kind,omitempty"`
    ID     string `json:"id,omitempty"`
    Status string `json:"status,omitempty"`
}

type EvidencePredicate struct {
    Kind  string                     `json:"kind"`
    Facts map[string]json.RawMessage `json:"facts,omitempty"`
}
```

Use `map[string]json.RawMessage` for requirement and predicate facts. Raw JSON gives exact JSON comparison semantics and avoids accidental `float64` normalization problems from `map[string]interface{}`.

Extend evidence kind specs:

```go
type KindSpec struct {
    Description string         `json:"description,omitempty"`
    Class       string         `json:"class,omitempty"`
    Facts       *FactsContract `json:"facts,omitempty"`
}

type FactsContract struct {
    Required   []string                `json:"required,omitempty"`
    Properties map[string]FactProperty `json:"properties,omitempty"`
}

type FactProperty struct {
    Type      string            `json:"type,omitempty"`
    Enum      []json.RawMessage `json:"enum,omitempty"`
    MaxLength int               `json:"maxLength,omitempty"`
    MaxItems  int               `json:"maxItems,omitempty"`
    ItemsType string            `json:"itemsType,omitempty"`
}
```

Supported v1 property types:

- `string`
- `boolean`
- `number`
- `integer`
- `array`

`itemsType` applies only when `type` is `array`, and should support scalar item types only.

Example:

```json
{
  "evidenceKinds": {
    "pre_interview_analysis": {
      "class": "analysis",
      "description": "Agent analysis before asking the user anything.",
      "facts": {
        "required": [
          "clarification_needed",
          "question",
          "default_recommendation"
        ],
        "properties": {
          "clarification_needed": { "type": "boolean" },
          "question": { "type": "string", "maxLength": 300 },
          "default_recommendation": { "type": "string", "maxLength": 500 }
        }
      }
    },
    "pressure_pass": {
      "class": "review",
      "facts": {
        "required": ["verdict"],
        "properties": {
          "verdict": {
            "type": "string",
            "enum": ["ready", "needs_patch", "too_vague"]
          }
        }
      }
    }
  }
}
```

## Template Validation

`ValidateTemplate` must validate facts contracts when templates are installed or validated.

Reject:

- invalid property types
- `required` entries that do not reference declared properties
- enum values that do not match the declared property type
- `itemsType` on non-array properties
- invalid `itemsType`
- negative `maxLength` or `maxItems`
- malformed facts predicates in `requires.evidence.facts`
- malformed facts predicates in `predicate.evidenceExists.facts`
- transition requirements that reference unknown evidence kinds, preserving current behavior

Example errors:

```text
evidence kind pressure_pass facts.properties.verdict has invalid type "str"
evidence kind pressure_pass facts.required[0] references undeclared property "verdict"
evidence kind pressure_pass facts.properties.verdict enum value 3 does not match type string
evidence kind foo facts.properties.tags itemsType is only valid for array properties
transition finalize_ready_pbc requires pressure_pass fact verdict, but pressure_pass does not declare that fact
```

Do not silently ignore malformed contracts. An installed template stores a canonical typed definition; misspelled fields becoming no-ops would be hard to debug.

## Facts Validation

Facts validation happens in `AddEvidence`.

Rules:

- If `facts` is present, it must be a JSON object.
- Facts must be flat.
- Nested objects are rejected.
- Arrays are allowed if they contain scalar values only.
- Unknown additional facts are allowed if they are flat.
- Declared fields, when present, must satisfy their declared validation.
- Required fields must be present when the kind has a facts contract.
- If a known kind has no facts contract, facts are still allowed but must be a flat object.
- If an unknown kind is accepted by current behavior, facts are still allowed but must be a flat object.
- `data_json` remains freeform valid JSON only.

Validation matrix:

```text
known kind + no facts contract + no facts        -> accept
known kind + no facts contract + valid facts     -> accept
known kind + facts contract + valid facts        -> accept
known kind + facts contract + missing facts      -> reject if required fields exist
known kind + facts contract + invalid facts      -> reject
unknown kind + no facts                          -> accept, preserving current behavior
unknown kind + valid flat facts                  -> accept
any kind + facts not an object                   -> reject
any kind + facts containing nested object        -> reject
```

Examples:

```text
evidence pressure_pass facts.verdict must be one of ready, needs_patch, too_vague
evidence pre_interview_analysis missing required fact clarification_needed
evidence foo facts must be a JSON object
evidence foo facts.extra must be flat; nested objects are not supported
```

Number handling:

- Use `json.Decoder.UseNumber()` or raw canonical JSON comparison.
- Bias workflow routing facts toward strings and booleans.
- If `number` and `integer` are implemented, tests must cover `1` versus `1.0`, integer rejection for `1.2`, and enum comparison.

## AddEvidence API

Replace the positional `AddEvidence` signature:

```go
AddEvidence(taskSelector, kind, ref, summary, data, actor, role string)
```

with params:

```go
type AddEvidenceParams struct {
    TaskSelector string
    Kind         string
    Ref          string
    Summary      string
    Facts        string
    Data         string
    Actor        string
    Role         string
}

func (s *Service) AddEvidence(params AddEvidenceParams) (*Evidence, error)
```

This avoids call-site bugs as evidence creation grows.

`AddEvidence` should:

1. Resolve the latest workflow instance.
2. Load the installed template for that instance.
3. Find `evidenceKinds[params.Kind]`, if present.
4. Parse and validate `params.Facts`.
5. Parse and validate `params.Data` as valid JSON only.
6. Insert `workflow_evidence`.
7. Run existing evidence side effects such as delegated closure obligations.
8. Refresh the instance context hash in the same transaction.
9. Return the full evidence record, including facts and data when present.

## CLI Changes

Add `--facts` to both evidence creation paths.

```bash
wrkf evidence add T-123 \
  --kind pressure_pass \
  --ref agent:pressure-pass \
  --summary "needs patch" \
  --facts '{"verdict":"needs_patch"}' \
  --data '{"tighten":["Failure state vague"],"patch":["Failed: show retry and preserve selections."]}'
```

`wrkf evidence exec` must also accept `--facts`:

```bash
wrkf evidence exec T-123 \
  --kind verification \
  --facts '{"verdict":"pass"}' \
  -- make test
```

Without `--facts`, `evidence exec` would become unusable for evidence kinds with required facts. Adding the flag keeps both evidence creation paths equivalent.

## Matching Semantics

Implement one shared matcher and use it everywhere.

```go
func latestEvidenceByKind(ev []Evidence, kind string) (Evidence, bool)
func evidenceFactsMatch(e Evidence, required map[string]json.RawMessage) (ok bool, detail string)
```

Latest rule:

```text
Find the latest evidence for kind, ordered by produced_at then id.
If no latest evidence exists: missing.
If no facts condition is present: satisfied.
If facts condition is present: latest evidence must have facts_json object and all required keys must JSON-equal requested values.
```

This is intentionally not “any matching historical evidence.” The newest decision of that kind wins.

Use the matcher from:

- `transitionBlockers`
- `evalPredicate` for `evidenceExists`
- `SuggestEvidence`
- `Next` action generation and blocked transition reporting

Blocker messages should distinguish absence from mismatch:

```text
required evidence pressure_pass with facts verdict=ready is missing
required evidence pressure_pass with facts verdict=ready is blocked; latest verdict=needs_patch from ev_000014
required evidence pressure_pass with facts verdict=ready is blocked; latest evidence ev_000014 has no facts
```

## Transition Requirements

Existing requirement:

```json
{
  "requires": [
    {
      "evidence": {
        "kind": "pressure_pass"
      }
    }
  ]
}
```

New requirement:

```json
{
  "requires": [
    {
      "evidence": {
        "kind": "pressure_pass",
        "facts": {
          "verdict": "ready"
        }
      }
    }
  ]
}
```

If `facts` is omitted, preserve current presence-only behavior.

## Predicates

Extend `evidenceExists` predicates with the same facts matcher:

```json
{
  "evidenceExists": {
    "kind": "pre_interview_analysis",
    "facts": {
      "clarification_needed": true
    }
  }
}
```

This should use the same latest-evidence rule as transition requirements.

Do not merge evidence facts into generic `factEquals` yet. That would blur task facts, check facts, and evidence facts too early.

## SuggestEvidence And Next

`wrkf evidence suggest` should report facts requirements from the template:

```json
{
  "kind": "pressure_pass",
  "present": true,
  "latest": {
    "id": "ev_000014",
    "facts": {
      "verdict": "needs_patch"
    }
  },
  "requiredFacts": {
    "verdict": "ready"
  },
  "satisfied": false,
  "message": "latest verdict=needs_patch from ev_000014"
}
```

For a missing required kind:

```json
{
  "kind": "pressure_pass",
  "present": false,
  "requiredFacts": {
    "verdict": "ready"
  },
  "satisfied": false
}
```

`wrkf next` should use the same matching details for blocked transitions and recommended next actions.

## Context Hash Semantics

Current `contextHash` includes evidence as:

```go
refs = append(refs, e.Kind+":"+e.Ref)
```

That was tolerable when evidence requirements were presence-only. Once facts control routing, context hashing should include evidence IDs and fact hashes:

```go
refs = append(refs, e.ID+":"+e.Kind+":"+e.Ref+":"+Hash(e.Facts))
```

If data changes are immutable because evidence is append-only, hashing facts is enough for routing context. Including the evidence ID also distinguishes repeated decisions with the same kind/ref/facts.

`AddEvidence` should update the workflow instance context hash in the same transaction. Otherwise `--context` can fail to protect against a newer decision fact being added after the caller observed context.

Recommended behavior:

- Adding evidence does not increment workflow revision.
- Adding evidence does update `workflow_instances.context_hash`.
- Adding evidence updates `workflow_instances.updated_at`.
- Adding evidence updates the task meta workflow projection if needed.
- `--context` protects state, task doc, open obligations/effects, and evidence facts.

If this is too broad for the first implementation, document clearly that `--context` only protects state/task-doc changes. The stronger behavior is preferred because facts become part of transition routing.

## PBC Workflow Example

With structured evidence, the PBC preset can become more expressive.

Pre-interview analysis contract:

```json
{
  "pre_interview_analysis": {
    "class": "analysis",
    "facts": {
      "required": ["clarification_needed"],
      "properties": {
        "clarification_needed": { "type": "boolean" }
      }
    }
  }
}
```

Pressure pass contract:

```json
{
  "pressure_pass": {
    "class": "review",
    "facts": {
      "required": ["verdict"],
      "properties": {
        "verdict": {
          "type": "string",
          "enum": ["ready", "needs_patch", "too_vague"]
        }
      }
    }
  }
}
```

Branch to clarification:

```json
{
  "id": "ask_clarification",
  "requires": [
    { "evidence": { "kind": "behavior_note" } },
    {
      "evidence": {
        "kind": "pre_interview_analysis",
        "facts": { "clarification_needed": true }
      }
    }
  ]
}
```

Skip clarification:

```json
{
  "id": "draft_pbc",
  "requires": [
    { "evidence": { "kind": "behavior_note" } },
    {
      "evidence": {
        "kind": "pre_interview_analysis",
        "facts": { "clarification_needed": false }
      }
    }
  ]
}
```

Finalize only if pressure pass is ready:

```json
{
  "id": "finalize_ready_pbc",
  "requires": [
    {
      "evidence": {
        "kind": "pressure_pass",
        "facts": { "verdict": "ready" }
      }
    },
    { "evidence": { "kind": "pbc_final" } }
  ]
}
```

Request patch only if pressure pass says patch:

```json
{
  "id": "request_patch_decision",
  "requires": [
    {
      "evidence": {
        "kind": "pressure_pass",
        "facts": { "verdict": "needs_patch" }
      }
    }
  ]
}
```

## Implementation Order

1. Add migration `000019_workflow_evidence_facts.sql`.
2. Update evidence domain types and every evidence scanner/loader/output path.
3. Add named requirement structs.
4. Add `KindSpec.Class`, `KindSpec.Facts`, `FactsContract`, and `FactProperty`.
5. Validate facts contracts during template validation.
6. Replace `AddEvidence` positional strings with `AddEvidenceParams`.
7. Add `--facts` to `wrkf evidence add`.
8. Add `--facts` to `wrkf evidence exec`.
9. Implement facts parser/validator.
10. Validate facts inside `AddEvidence`.
11. Implement shared latest-evidence facts matcher.
12. Use matcher in transition blockers, evidence predicates, suggestion, and next-action generation.
13. Update context hash behavior and refresh context on evidence add.
14. Update snapshot/export/import behavior if workflow evidence is included there.
15. Add tests and smoke coverage.
16. Update PBC preset to `pbc-progressive-refinement@3` using facts predicates.

## Tests

Compatibility:

- Old template without facts still validates.
- Old evidence without facts still lists/shows/transitions.
- Presence-only evidence requirements still work.
- Known kind with no facts contract accepts valid facts object.
- Unknown kind accepts valid facts object.

Facts validation:

- Facts omitted for kind with required facts rejects.
- Facts present as `[]` rejects.
- Facts present as `"string"` rejects.
- Facts with nested object rejects.
- Facts with arrays of scalar values accepts.
- Facts with arrays containing objects rejects.
- Required field missing rejects.
- Invalid enum rejects.
- Invalid string maxLength rejects.
- Unknown flat fields are accepted.

Numeric validation:

- Integer fact accepts `1`.
- Integer fact rejects `1.2`.
- Integer enum compares `1` safely.
- Number enum behavior is documented and tested.
- `1` versus `1.0` behavior is explicit.

Template validation:

- Invalid facts property type rejects.
- Required field referencing undeclared property rejects.
- Enum value mismatched with declared type rejects.
- `itemsType` on non-array rejects.
- Invalid `itemsType` rejects.
- Requirement fact key not declared by kind contract rejects, if strict declaration is adopted.

Matching:

- Older `pressure_pass` verdict `ready` plus newer `needs_patch` blocks ready transition.
- Older `needs_patch` plus newer `ready` passes ready transition.
- Latest evidence with no facts blocks fact requirement.
- Latest evidence with mismatched facts blocks fact requirement.
- Presence-only requirement is satisfied by latest evidence regardless of facts.
- `evidenceExists.facts` uses the same latest rule as `requires.evidence.facts`.

CLI:

- `wrkf evidence add --facts` stores and returns facts.
- `wrkf evidence exec --facts` stores and returns facts.
- `wrkf evidence exec` with a facts-contract kind and missing required facts rejects.

Suggestion and next:

- `SuggestEvidence` reports missing kind.
- `SuggestEvidence` reports latest mismatch, not only `present=false`.
- `wrkf next` reports blocked fact requirements with latest evidence detail.

Context:

- Context hash changes after adding evidence facts.
- A transition with stale `--context` after newer evidence facts rejects.
- Context hash includes evidence ID and facts hash.

PBC smoke:

- `ask_clarification` passes only when latest `pre_interview_analysis.clarification_needed=true`.
- `draft_pbc` passes only when latest `pre_interview_analysis.clarification_needed=false`.
- `finalize_ready_pbc` passes only when latest `pressure_pass.verdict=ready`.
- `request_patch_decision` passes only when latest `pressure_pass.verdict=needs_patch`.

## Non-Goals

- Full JSON Schema support.
- Global schema registry.
- External schema files.
- Nested facts objects.
- Validating full artifact body structure.
- Migrating old evidence records.
- Merging evidence facts with task facts or check facts.

## Recommendation

Adopt structured evidence facts as an additive wrkf primitive.

The two critical refinements are:

1. Store and compare fact predicates as raw JSON, and centralize latest-evidence matching so transition blockers, predicates, suggestions, and next actions cannot drift.
2. Treat evidence facts as workflow context by hashing evidence IDs and facts into `context_hash`, and refresh that hash when evidence is added.

This keeps wrkf small while giving workflow templates enough typed surface area to make decisions without relying on prose conventions.
