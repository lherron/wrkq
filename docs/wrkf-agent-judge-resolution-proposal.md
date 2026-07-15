# WRKF Agent-Judged Resolution

Status: implementation-ready proposal; Daedalus approved revision 5
Date: 2026-07-15
Scope: `wrkq` / WRKF engine, `@wrkq/client`, `@praesidium/agent-loop`, and `loops/wrkf-task-loop`

Architecture review: **APPROVED** by `daedalus@wrkq:primary` in
`msg-31a7a728-76bb-433a-b551-5c89cbd063a8` after four contract-tightening passes.
The ruling found revision 5 implementation-ready. It proposes future guarded law but
does not activate or amend current architecture records before their live guards and
evidence exist.

## Recommendation

Add **judged resolution** as an opt-in, two-stage executable-action protocol:

```text
work candidate
  -> action claim
  -> worker/seat produces observations and a proposed outcome
  -> action submit (durable, no workflow transition)
  -> judge candidate
  -> judge claim
  -> separately scoped agent returns a structured verdict
  -> judge settle (atomic evidence + transition + terminalization)
```

WRKF must support and fence this protocol, but it must never invoke an LLM or
interpret prose. It records the worker submission, projects a judge candidate,
validates the judge's structured verdict against the template contract, and
atomically applies the selected outcome. `agent-loop` owns the actual judge turn.

This is not “let the model call `settle`.” The judge never receives an owner token,
database access, a mutable checkout, or tools. A trusted runner holds the lease,
materializes the immutable judgment bundle, invokes a separately scoped tool-denied
agent, validates the JSON reply, and submits it to WRKF.

For `wrkq-simple-task`, introduce `@6` rather than mutating live `@5`. The `@6`
template folds `test_review` into judged settlement of `test` and folds `gate` into
judged settlement of `verify`. The crank gains a dedicated `room-judge` and can keep
turning through approved outcomes without asking the coordinator to manually claim
and settle every review action.

## Why this is the right boundary

The current system has the right durable authority but conflates observation with
adjudication:

- WRKF v5 correctly makes `action.next -> action.claim -> action.settle` the semantic
  path, preserves exact source identity, and treats lease expiry as contestability
  rather than failure.
- `wrkf-task-loop` correctly measures git/runtime facts outside the agent reply.
- A seat still returns a semantic `result`, and the crank immediately writes that
  value as transition-driving evidence in `action.settle`.
- v5 compensates at two points with explicit coordinator-owned `test_review` and
  `gate` phases. The crank halts on those candidates and prints a manual settlement
  skeleton.
- The engine's ratified doctrine is “records, never interprets.” Moving an LLM call
  into WRKF would violate that boundary and make the database/runtime inseparable.

An in-memory judge call inserted immediately before today's `action.settle` would be
small, but it would not be durable. A crash after side effects and before settlement
would lose the exact evidence bundle and verdict context. It would also let a runner
silently bypass judging. The worker submission therefore needs a durable boundary of
its own.

## Canonical model

### Terms

| Term | Meaning | Owner |
|---|---|---|
| Execution claim | Temporary fenced ownership of a run attempt. This is today's WRKF lease claim. | WRKF |
| Submission | Immutable observations, artifacts, mechanical facts, and optional proposed outcome produced by a work run. | WRKF record; runner produces |
| Proposed outcome | The worker's semantic assertion about what its submission means. It is advisory until judged. | Worker |
| Resolution request | Durable record that a submitted action awaits judgment under a named policy. | WRKF |
| Judgment bundle | Canonical, hash-bound snapshot of the task, action, source binding, submission, cited prior evidence, allowed outcomes, and policy refs. | WRKF defines; runner materializes |
| Judge run | Separately claimed run attempt that evaluates one resolution request. | WRKF lease; agent-loop executes |
| Verdict | Structured judge output selecting one template-allowed outcome with rationale and citations. | Judge agent |
| Settlement | The only operation that changes workflow truth: final result evidence, transition/suspension, effects, and run/request terminalization in one transaction. | WRKF |

Do not call the worker's semantic assertion a “claim” in APIs. `claim` already means
lease ownership. Use `submission.proposedOutcome` or `settlementProposal` so logs and
operators cannot confuse epistemic claims with execution authority.

### Invariants

1. **The engine stays deterministic.** WRKF never invokes a model, parses rationale,
   scores confidence, or chooses an outcome.
2. **Direct settlement remains the default.** Existing templates and direct/system
   actions keep today's claim/settle behavior.
3. **A judged work run cannot complete directly.** It must submit; a completed
   direct settle or transition attempt is refused with `WRKF_RESOLUTION_REQUIRED`.
   Existing downgrade settlements may terminalize an operationally failed or
   cancelled work attempt, but they cannot create final result evidence or advance
   the workflow.
4. **Submission does not change workflow truth.** It writes evidence, terminalizes
   the work attempt as `submitted`, releases its token, and creates one pending
   resolution request. Instance state and revision do not change.
5. **Judge settlement is the sole semantic commit path.** For any judged action
   occurrence, generic evidence addition, work-run settle/complete, and public
   transition application cannot create the final result evidence or apply the
   subject transition. After submission, the pending resolution is also the sole
   executable candidate.
6. **Judge ownership is separately fenced.** Judge attempts use the same lease,
   predecessor acknowledgement, supersession, and late-settle laws as work attempts.
7. **Separation of duty uses the existing policy.** The subject transition uses
   `separationOfDuty.distinctPrincipalFromEvidence=[submissionEvidenceKind]`, enforced
   at judge claim and settle. No parallel policy vocabulary is introduced.
8. **Mechanical facts are never authored by the judge.** Commit SHAs, source identity,
   hashes, exit codes, paths, and runtime IDs come from the submission or server-side
   copying rules. The LLM selects an outcome and explains it.
9. **Exact source identity remains authoritative.** Commit hash or artifact digest is
   the semantic source key. Run IDs remain provenance and linkage only.
10. **The verdict is snapshot-bound.** The trusted runner supplies the expected
    WRKF-computed bundle hash outside model output. Candidate input, template
    definition, instance revision, task document, and the bundle's finite citation set
    are server-persisted authority. Stale or mismatched snapshots fail closed.
11. **Settlement stays atomic.** Final evidence, transition or suspension, durable
    effects and obligations, resolution request, judge run, and owner-token cleanup
    commit together or not at all.
12. **Staleness cannot deadlock recovery.** A stale request can never complete, but a
    successor may still supersede an active/expired judge into an explicitly
    recovery-only attempt whose sole legal settlement is `cancelled`.
13. **No invisible retries.** Every judge retry names the preceding judge run. Every
    re-execution after a cancelled submission names the preceding work run.
14. **No unbounded crank.** A single crank invocation has a hard unit budget. The
    work-to-judge continuation for one request is allowed; the subject is marked seen
    only after judged settlement, and a later return to it halts before re-execution.

## Template contract

Add an optional `resolution` block to `ExecutableActionSpec`. Absence means `direct`.

```json
{
  "executableActions": {
    "verify": {
      "role": "observer",
      "transition": "verify_complete",
      "handlerContract": "praesidium.wrkq-simple-task.verify@6",
      "resultEvidenceKind": "verify_result",
      "sideEffectClasses": ["install", "smoke", "test"],
      "sourceBinding": {
        "kind": "previous_action",
        "action": "implement",
        "requiredFacts": ["source_identity"],
        "bindFields": { "sourceIdentity": "source_identity" }
      },
      "submissionValidation": {
        "rules": [{
          "whenFacts": { "proposedOutcome": "pass" },
          "identityFact": "source_identity",
          "linkageFact": "source.evidence_id",
          "requiredFacts": [
            "source_identity",
            "source.evidence_id",
            "verified.head.sha",
            "git.clean"
          ]
        }]
      },
      "resolution": {
        "mode": "agent_judge",
        "submissionEvidenceKind": "verify_submission",
        "judgeRole": "judge",
        "judgeHandlerContract": "praesidium.wrkf.agent-judge@1",
        "policyRef": "wrkq-simple-task.verify@6",
        "requiredCitations": ["submission"],
        "carryFacts": [
          "source_identity",
          "source.evidence_id",
          "verified.head.sha",
          "git.clean"
        ]
      }
    }
  },
  "transitions": [{
    "id": "verify_complete",
    "from": { "status": "active", "phase": "verify" },
    "by": ["judge"],
    "separationOfDuty": {
      "distinctPrincipalFromEvidence": ["verify_submission"]
    },
    "outcomes": [
      {
        "id": "pass",
        "when": { "evidenceExists": {
          "kind": "verify_result", "facts": { "result": "pass" }
        }},
        "to": { "status": "active", "phase": "land" }
      },
      {
        "id": "failed",
        "when": { "evidenceExists": {
          "kind": "verify_result", "facts": { "result": "failed" }
        }},
        "to": { "status": "active", "phase": "implement" }
      },
      {
        "id": "operator_required",
        "when": { "evidenceExists": {
          "kind": "verify_result", "facts": { "result": "operator_required" }
        }},
        "suspend": { "reason": "operator_required" }
      }
    ]
  }]
}
```

Rules:

- `mode` is `direct` or `agent_judge`; omission is `direct`.
- `submissionEvidenceKind`, `judgeRole`, `judgeHandlerContract`, and `policyRef` are
  required for `agent_judge`.
- `submissionEvidenceKind` and `resultEvidenceKind` must be different. The former is
  worker-authored; the latter is judge-authored and transition-driving.
- `submissionValidation` uses the existing settlement-rule vocabulary but runs at
  submit time against worker/mechanical facts. Existing `settleValidation` continues
  to guard the final result evidence after WRKF copies declared carry facts.
- Allowed judge outcomes are derived from the action's declared transition outcomes.
  Do not duplicate them in the resolution block.
- Every outcome must be an explicit one-to-one arm whose id equals the final
  `resultEvidenceKind`'s `facts.result`; judged transitions may not use `otherwise`.
- The subject transition's `by` role and final result evidence producer are the
  declared judge role.
- The transition must use the existing
  `separationOfDuty.distinctPrincipalFromEvidence` policy against the submission
  evidence kind. WRKF enforces it when the judge candidate is claimed and again when
  it settles.
- `carryFacts` must name facts declared by both evidence schemas. WRKF copies them
  byte-for-byte from the submission into final result evidence and records the source
  evidence id. Judge output cannot override them.
- `requiredCitations` is structural. In v1, `submission` means the verdict must cite
  the resolution request's submission evidence id.
- `policyRef` and handler contracts are names, not prompt text or model provenance.
  The source-controlled runner owns their implementation.

Template validation must reject:

- a judged action without a submission/result evidence pair;
- unknown judge roles or handler contracts with empty versions;
- carry facts absent from either schema;
- a resolution policy whose transition has no finite outcome set;
- a judged transition with `otherwise`, mismatched outcome id/result fact, a non-judge
  `by` role, or no separation-of-duty rule against the submission kind;
- a policy with no suspension/escape outcome when all ordinary outcomes require
  evidence the judge may find insufficient;
- a source-binding fact that would be supplied only by the LLM rather than carried
  from measured submission evidence.

## Engine protocol

### Candidate shape

Extend the existing action candidate rather than introduce a competing scheduler API.

```ts
interface WrkfActionCandidateBase {
  instanceId: string
  task: string
  semanticActionKey: string
  action: string
  transition: string
  role: string
  requiredEvidenceKind: string
  expectedStateRevision: number
  expectedState: WrkfState
  expectedTaskDocHash?: string
  inputHash?: string
  source?: WrkfActionSourceBinding
  handlerContract?: string
  workspaceMode?: "none" | "read-only" | "exclusive" | (string & {})
  workspaceRef?: string
  sideEffectClasses?: string[]
  rank: number
  blocked?: boolean
  blockedReason?: string
  templateDefinitionHash: string
  [k: string]: unknown
}

interface WrkfDirectWorkCandidate extends WrkfActionCandidateBase {
  executionKind: "work"
  resolution?: never
}

interface WrkfJudgedWorkCandidate extends WrkfActionCandidateBase {
  executionKind: "work"
  expectedTaskDocHash: string
  inputHash: string
  resolution: {
    mode: "agent_judge"
    submissionEvidenceKind: string
    policyRef: string
  }
}

interface WrkfJudgeCandidate extends WrkfActionCandidateBase {
  executionKind: "judge"
  expectedTaskDocHash: string
  inputHash: string
  resolution: {
    mode: "agent_judge"
    requestId: string
    subjectAction: string
    subjectRunId: string
    subjectCandidateInputHash: string
    submissionEvidenceId: string
    bundleHash: string
    policyRef: string
    allowedOutcomes: string[]
    requiredCitations: string[]
    snapshotStatus: "current" | "stale"
    staleFields?: Array<
      "candidate_input" | "template_definition" | "instance_revision" | "task_document"
    >
  }
}

type WrkfActionCandidate =
  | WrkfDirectWorkCandidate
  | WrkfJudgedWorkCandidate
  | WrkfJudgeCandidate
```

This is an **additive refinement of the full current candidate shape**: `transition`,
workspace fields, side-effect classes, rank, blocking fields, and their existing
optional/forward-compatibility semantics remain intact. The only additions are
`executionKind`, `templateDefinitionHash`, and the discriminated `resolution` arms.
The union is the complete routing contract. A generic runner branches first on
`executionKind`; in the work arm, the presence of
`resolution.mode="agent_judge"` selects `action.submit`, while absence selects today's
`action.settle`. It must not infer resolution mode from action names, template ids, or
an out-of-band `@6` registry. WRKF JSON-RPC, `@wrkq/client`, and the canonical
engine-runner fixture freeze this complete additive union.

For a judged work candidate, the routing descriptor exists before any request and
contains only the submission kind and policy ref needed to choose the protocol. For a
judge candidate, the descriptor is request-bound:

- `action` remains exactly the subject action (for example, `verify`); only
  `executionKind="judge"` distinguishes the attempt. This preserves today's
  `runActionByIDQuery`/`sourceBinding.action` semantic identity, so judged
  `verify_result` remains consumable by landing;
- `semanticActionKey` is `resolve:<requestId>` so judge retries share one lineage;
- `role` and `handlerContract` come from the resolution policy;
- `source` remains the original action's exact-source binding;
- the full judgment bundle is available from `wrkf.resolution.show`; `action.next`
  carries only routing and hash-bound summary fields.

The judge descriptor's `snapshotStatus` is a server comparison between the pending
request and current candidate/template/instance/task authority. `current` permits the
normal judge protocol. `stale` is still an executable recovery projection, but it is
never judgeable: the generic crank must not materialize artifacts or invoke a model.
`staleFields` is a bounded mechanical explanation for the coordinator, not model
input. Do not overload the existing `blocked`/`blockedReason` compatibility fields for
this; the pending request remains the sole candidate so its recovery path stays
visible.

Every candidate also carries the hash of the **currently stored canonical template
definition**, not merely `id@version` or the instance's historical pin. Include that
`templateDefinitionHash` and the complete projected `resolution` descriptor in the
canonical `inputHash` payload. This closes same-version embedded-template replacement
across claim and submit without teaching the runner template semantics. Reuse the
existing `ShowTemplate`/`workflow_templates.hash` value; do not introduce a second
template hashing algorithm.

### `wrkf.action.submit`

New fenced mutation for judged work runs.

At the preceding work claim, WRKF persists the server-projected candidate snapshot on
the run: candidate `inputHash` as `candidateInputHash`, plus
`expectedStateRevision`, `expectedTaskDocHash`, and `templateDefinitionHash`.
`action.submit` reloads the current template definition and reprojects the same subject
work candidate. It requires the current template hash, recomputed candidate input hash,
instance revision, and task hash to equal the server-recorded claim snapshot; it does
not trust a caller to echo any of them. Resolution policy, evidence schema, source
binding, state, or task drift is non-retryable `WRKF_RESOLUTION_STALE` with no write. A
claim followed by a task/spec or same-version template edit therefore cannot silently
submit under the old contract.

```ts
interface WrkfActionSubmitParams {
  runId: string
  ownerToken: string
  ownerGeneration: number
  submission: WrkfActionEvidenceInput
  terminalSummary?: string
}

interface WrkfActionSubmitResult {
  run: WrkfWorkflowRunAttempt       // status="submitted", token cleared
  evidence: WrkfEvidence
  resolution: WrkfResolutionRequest // status="pending"
}
```

One transaction must:

1. validate current owner authority and reject superseded runs;
2. confirm the action declares `agent_judge` resolution;
3. validate the submission evidence schema, source linkage, and submission-specific
   mechanical rules;
4. write immutable run-linked submission evidence;
5. create exactly one pending resolution request with the subject candidate input
   hash, template definition hash, instance revision, task hash, source binding,
   allowed outcomes, policy refs, and the versioned canonical bundle schema, exact
   bytes, and hash;
6. set the work run to terminal status `submitted` and revoke its token;
7. emit `workflow.action_submitted`.

If the worker makes an advisory semantic assertion, it appears exactly once as the
submission evidence fact `proposedOutcome`. There is no duplicate top-level submit
field.

The operation is idempotent by work run and canonical payload. A different replay is
`WRKF_IDEMPOTENCY_MISMATCH`. For an already-`submitted` run, exact replay lookup and
payload comparison happen before live snapshot validation, so a lost successful
response returns the original request even if the world later becomes stale; it never
writes again.

### `wrkf.action.next` and `wrkf.action.claim`

When an instance has a pending resolution request, `action.next` returns its judge
candidate as the **only** candidate instead of re-projecting the subject work action.
The crank's existing exactly-one-candidate rule remains valid.

The judged action contract fences every alternate semantic commit door from the
moment the action occurrence is projected, not merely after a request exists. Generic
`wrkf.evidence.add` may not create that action's final `resultEvidenceKind`;
work-run `action.settle`/legacy `action.complete` may not apply its transition; and the
public transition API may not apply the subject transition. Only settlement of the
claimed judge run for the pending request may write the resolution-linked final
evidence and transition.

`action.claim` handles the judge candidate with the current claim-succession law:

- before writing a judge run, revalidate the request's template-definition hash,
  subject candidate input hash, instance revision, and task hash against current
  server state;
- first claim explicitly acknowledges `priorRun: null`;
- a retry must name the latest judge run;
- expiry only makes the judge claim contestable;
- supersession, not expiry, revokes a late judge settlement;
- the refusal includes the predecessor's full record and evidence refs;
- transition separation-of-duty is checked at claim using the canonical principal on
  the submission evidence. A matching producer principal is refused before authority
  is issued.

For a current request, claim proceeds normally. For a stale request with no
active/expired judge predecessor, normal claim is refused with
`WRKF_RESOLUTION_STALE`; the request can be cancelled directly. For a stale request
whose latest judge is still active (expired leases included), ordinary stale refusal
would deadlock recovery. In that one case, a claim that explicitly names the exact
latest predecessor is allowed to run the existing succession transaction: supersede
the predecessor, revoke its token, append normal ledger lineage, and create a new
`resolutionRecoveryOnly=true` judge run. A missing/wrong `priorRun` still receives the
ordinary lease conflict and predecessor record.

The recovery-only binding carries the request's stale status/reasons. It grants no
semantic authority: `run.bindExternal`, `action.complete`, `action.fail`, evidence
write paths, and any completed/operational settlement are refused; its sole legal
terminal mutation is `action.settle(result="cancelled")`. This is a narrow recovery
capability implemented on the same claim table, not a lease-law exception.

The judge run is stored in `workflow_runs` with `execution_kind="judge"` and a
`resolution_request_id`. Its `action` remains the subject action, its role is the
template's judge role, and its semantic action key is `resolve:<requestId>`. It never
receives the subject work run's owner token. Existing action-role validation must
branch on `execution_kind`: a work run matches the executable action's work role,
while a judge run matches `resolution.judgeRole`. That exception changes role
authority only; it does not change the run's subject `action` or source identity.

### `wrkf.action.settle`

Keep today's direct behavior for `executionKind="work"` actions whose resolution mode
is direct. For a judged work action, direct settlement is rejected. For a judge run,
the same method accepts a verdict:

```ts
interface WrkfJudgeVerdictInput {
  outcome: string
  rationale: string
  citedEvidenceIds: string[]
  findings?: Array<{
    severity: "info" | "warning" | "blocking"
    summary: string
    evidenceIds: string[]
  }>
}

interface WrkfActionSettleParams {
  runId: string                 // judge run id
  ownerToken: string
  ownerGeneration: number
  result: "completed" | "operational_failed" | "cancelled"
  expectedBundleHash?: string    // trusted runner input, never model output
  verdict?: WrkfJudgeVerdictInput
  evidence?: WrkfActionEvidenceInput // direct/downgrade paths only
}
```

If the run is `resolutionRecoveryOnly`, WRKF accepts only `result="cancelled"` with no
verdict, final evidence, or transition. `result="completed"` remains
`WRKF_RESOLUTION_STALE`; any other result is validation failure. Cancellation
terminalizes that attempt and clears its token, after which `resolution.cancel` may
cancel the still-pending request.

For `result="completed"`, WRKF must atomically:

1. validate judge lease authority and the transition's existing separation-of-duty
   rule against the submission principal;
2. load the pending request and compare runner-supplied `expectedBundleHash`
   byte-for-byte;
3. revalidate the current template-definition hash, recomputed subject candidate input
   hash, instance revision, and task document hash against the request;
4. validate `outcome` against the transition's finite one-to-one outcome set and use
   it as final `facts.result` without parsing the rationale;
5. validate required citations and require every `citedEvidenceId` and every finding's
   `evidenceId` to be a member of the finite citation-id set persisted in the canonical
   request bundle; a merely known or cross-instance evidence id is invalid;
6. create the action's final `resultEvidenceKind` record with judge-authored verdict
   fields, WRKF-copied carry facts, and a link to the submission;
7. apply the subject action's transition using that final evidence;
8. mark the resolution request resolved and the judge run completed;
9. write durable effect/obligation rows, apply internal projections, and clear judge
   authority in the same transaction.

For a completed judge run, the caller supplies `verdict` and must not supply an
independently authored result-evidence payload. WRKF constructs the final result
evidence from the verdict plus server-copied carry facts. `evidence` remains available
for current direct settlement and operational-failure paths.

The runner binds the actual judge runtime through the existing judge run
`externalRunRef` surface before settlement. Runtime identity and bundle hash are never
fields the model can author.

For `operational_failed`, terminalize only the judge attempt with `failure_result`
evidence. Leave the resolution request pending so `action.next` offers another judge
attempt with explicit predecessor acknowledgement. A model timeout or invalid JSON is
not a semantic verdict.

For `cancelled`, terminalize only the currently owned judge attempt and leave the
resolution request pending. Request cancellation is a separate recovery step described
below.

External effect delivery remains post-commit and replay-safe. If the transaction
commits but the response or subsequent delivery is lost, replay adopts the committed
settlement and resumes effect delivery; it never invokes the judge again or writes a
second verdict. As with submit, terminal-run replay recognition precedes live snapshot
checks and only returns already-committed state.

### Resolution read and recovery

Add narrow read/recovery methods:

```text
wrkf.resolution.show
wrkf.resolution.list
wrkf.resolution.cancel
```

`show` returns the canonical judgment bundle and its hash. `list` supports instance,
task, status, and action filters. Cancellation takes
`{requestId, principal_ref, reason, idempotencyKey?}`; `reason` and the standard
canonicalized caller principal are durable request fields. `cancel` is an explicit
coordinator recovery action:
it marks an unresolved request cancelled, records a reason and principal, and makes the
subject work action eligible again. It does not advance workflow state. The next work
claim still has to name the submitted predecessor, so re-executing side effects cannot
happen invisibly.

`resolution.cancel` refuses while any judge run for the request is active, even when
its lease has expired. Recovery preserves `wrkq.wrkf-action.lease-recovery` in this
order:

1. read the judge candidate's `snapshotStatus`; never invoke a model when it is
   `stale`;
2. claim a successor judge attempt through normal `priorRun` acknowledgement; when
   stale, WRKF marks this attempt `resolutionRecoveryOnly` while atomically
   superseding/revoking the predecessor;
3. settle that owned recovery-only judge attempt as `cancelled`, leaving the request
   pending;
4. cancel the now-unclaimed request with canonical principal and reason.

The request-cancel transaction rechecks that no active judge run exists. It never
silently revokes an active/expired token.

If a stale request has no active judge, skip the attempt sequence and cancel the
request directly. If drift happens between `action.next` and `action.claim`, the claim
response or typed stale refusal still prevents a model call; a returned recovery-only
binding must be handed back to the coordinator rather than executed as judgment.

Use `cancel` for a stale task document, corrupted/missing artifact, policy contract
withdrawal, or an operator decision to redo the work. Do not automatically cancel on
judge failure.

### Errors

Add stable errors:

| Code | Retryable | Meaning |
|---|:---:|---|
| `WRKF_RESOLUTION_REQUIRED` | false | A judged work run attempted direct settlement. |
| `WRKF_RESOLUTION_NOT_FOUND` | false | Request id does not exist. |
| `WRKF_RESOLUTION_STALE` | false | Candidate input, template definition, instance revision, task hash, or bundle hash no longer matches; cancel and redo the request. |
| `WRKF_RESOLUTION_CONFLICT` | true | Pending-request uniqueness or request terminal state conflicts with the requested operation. |
| `WRKF_VERDICT_INVALID` | false | Outcome, citation, or verdict evidence violates the template contract. |

Separation-of-duty failures use the existing transition blocker/error contract; do not
add a judge-specific duplicate. Do not overload lease errors for verdict/schema
failures. Claim succession and cancellation refusal while an active/expired judge run
still owns authority use the existing `WRKF_LEASE_CONFLICT` shape and predecessor
record.

## Storage and audit

Add one semantic table and reuse existing run/evidence ledgers.

```sql
workflow_resolution_requests (
  id                    TEXT PRIMARY KEY,
  instance_id           TEXT NOT NULL,
  subject_run_id        TEXT NOT NULL,
  subject_action        TEXT NOT NULL,
  semantic_action_key   TEXT NOT NULL,
  submission_evidence_id TEXT NOT NULL,
  policy_ref            TEXT NOT NULL,
  judge_role            TEXT NOT NULL,
  judge_handler_contract TEXT NOT NULL,
  subject_candidate_input_hash TEXT NOT NULL,
  template_definition_hash TEXT NOT NULL,
  expected_revision     INTEGER NOT NULL,
  expected_task_hash    TEXT NOT NULL,
  bundle_schema         TEXT NOT NULL, -- wrkf.judgment-bundle/v1
  bundle_bytes          BLOB NOT NULL, -- persisted canonical UTF-8 JSON bytes
  bundle_hash           TEXT NOT NULL,
  allowed_outcomes_json TEXT NOT NULL,
  carry_facts_json      TEXT NOT NULL,
  status                TEXT NOT NULL, -- pending|resolved|cancelled
  resolved_by_run_id    TEXT,
  result_evidence_id    TEXT,
  outcome               TEXT,
  created_at            TEXT NOT NULL,
  resolved_at           TEXT,
  cancelled_at          TEXT,
  cancellation_reason   TEXT,
  cancellation_principal_ref TEXT,
  UNIQUE(subject_run_id)
)

CREATE UNIQUE INDEX workflow_resolution_one_pending_subject
ON workflow_resolution_requests(instance_id, semantic_action_key)
WHERE status = 'pending';
```

Add `execution_kind`, nullable `resolution_request_id`, and
`resolution_recovery_only` (false by default) to `workflow_runs`; project the last
field on `WrkfWorkflowRunAttempt`/the fenced binding. Reuse all current lease, owner,
predecessor, supersession, external runtime, workspace ref, and evidence linkage
columns. Also persist the work candidate snapshot as
`candidate_input_hash`, `template_definition_hash`, `expected_state_revision`, and
`expected_task_doc_hash` on the claimed work run. Do not create a second claim/lease
table.

Add `submitted` as a terminal **attempt** status only for judged work runs. Update run
reads, terminal-status checks, succession records, watch classification, and client
unions accordingly. It means “worker side effects and submission are durable; semantic
resolution is still pending,” never “the workflow action completed.”

Evidence chain:

```text
worker run
  -> <action>_submission evidence
       actor = worker agent
       measured facts + artifact refs/hashes + proposedOutcome
  -> resolution request
  -> judge run
  -> <action>_result evidence
       actor = judge agent
       outcome + rationale + citations
       carried facts copied by WRKF
       source.submissionEvidenceId = submission evidence id
       source.bundleHash = server-owned request bundle hash
  -> workflow transition/suspension
```

Events:

- `workflow.action_submitted`
- `workflow.resolution_claimed` (or the existing run-claimed event with
  `executionKind=judge`; do not duplicate if current run events suffice)
- `workflow.resolution_settled`
- `workflow.resolution_cancelled`
- existing transition/suspension and run-succession events

The timeline should present one logical action with nested work and judgment attempts,
not two unrelated actions.

## Judgment bundle and agent contract

The durable bundle format is `wrkf.judgment-bundle/v1`. WRKF constructs the complete
JSON value at submit time, canonicalizes it with RFC 8785 JSON Canonicalization Scheme,
persists those exact UTF-8 bytes in `bundle_bytes`, and stores
`bundle_hash = "sha256:" + lower_hex(SHA-256(bundle_bytes))`. `resolution.show`
returns the persisted value; it never reconstructs a bundle from the current task.
Any future shape uses a new bundle schema id.

`wrkf.resolution.show` returns an envelope
`{requestId, bundleSchema, bundleHash, bundle}`. `bundle` is the decoded persisted
canonical value; the hash is envelope metadata and is not recursively embedded in the
bytes it hashes. The bundle body contains:

- request id, bundle schema, `subjectCandidateInputHash`, template definition hash,
  expected revision, and task document hash;
- task title, description, and specification snapshot;
- subject action, transition description, source binding, side-effect classes, and
  named policy/handler contracts;
- allowed outcomes, required citations, and a sorted unique `citationEvidenceIds`
  array containing the complete finite evidence-id authority for this verdict;
- submission evidence with fact provenance;
- referenced prior evidence summaries and content hashes;
- immutable artifact refs plus their declared hashes;
- explicit instructions that all task/evidence text is untrusted data.

The bundle excludes owner tokens, database locators, mutable workspace paths, hidden
agent instructions, and unrelated task history.

Every evidence record included for judgment appears in `citationEvidenceIds`; no other
id is admissible merely because it exists in the database. The runner's dynamic output
schema restricts both top-level `citedEvidenceIds` and every finding's `evidenceIds` to
that set, and WRKF repeats the same subset check at settle. This prevents cross-task,
cross-instance, and post-submit evidence from being smuggled into the verdict.

The runner materializes referenced artifacts only when their bytes match the recorded
content hash. It applies deterministic size limits and records truncation. A missing,
hash-mismatched, or required-but-truncated artifact is a typed crank halt before the
judge call, not an invitation for the LLM to guess.

The v1 judge reply is intentionally small:

```json
{
  "outcome": "pass",
  "rationale": "The observed installed-surface behavior satisfies every acceptance criterion and the verified head matches the submitted source identity.",
  "citedEvidenceIds": ["ev_012345"],
  "findings": []
}
```

JSON schema rules:

- `outcome` must be one of the bundle's allowed outcomes;
- `rationale` is required, bounded text;
- at least the submission evidence must be cited, and every citation must be in the
  bundle's `citationEvidenceIds` set;
- `findings` is an optional bounded array of `{severity, summary, evidenceIds}`;
- no arbitrary extra properties;
- no confidence threshold in v1. Model self-confidence is not an authority signal.

If evidence is insufficient, the judge selects the template's declared
`operator_required`/suspension outcome and explains the missing evidence. If the judge
cannot produce schema-valid output at all, that is operational failure and retry, not
`operator_required`.

## Security and trust model

The feature is a structural correctness boundary, not a defense against a malicious
local RPC caller that can forge principal strings. Within that reality:

- use a dedicated installed `room-judge` agent, never the worker persona;
- invoke each verdict as a fresh one-shot `agent()` call with no continuation;
- default to `permissions: {mode: "deny"}`;
- never expose claim/settle tokens or DB access in the judge prompt;
- delimit task text, comments, logs, and artifact text as untrusted evidence;
- validate strict structured output and fail closed after the SDK's bounded repair;
- store judge agent ref, scope ref, the existing run `externalRunRef`, handler
  contract, policy ref, bundle hash, and cited evidence ids;
- keep model/prompt trace details in agent-loop artifacts rather than turning WRKF into
  a prompt provenance database;
- require exact source identity and mechanical gates before semantic judgment;
- never allow a verdict to override a failed hash, source-link, schema, lease, CAS, or
  suspension check.

## `@praesidium/agent-loop` changes

### Make client DTOs canonical

The current generic `packages/agent-loop/src/work/claimed-action.ts` redeclares WRKF
DTOs and still carries pre-v5 concepts such as workspace lease authority and
`commitSha` source binding. Meanwhile `wrkf-task-loop` correctly consumes
`@wrkq/client` directly.

Before adding judged resolution:

- delete local WRKF DTO mirrors from the public work helper;
- import/re-export the canonical `@wrkq/client` types;
- remove workspace lease heartbeat code killed by v5;
- use `sourceIdentity`, not a commit-specific field, while preserving commit/artifact
  values as the actual identity payload;
- add `priorRun` support so helper behavior matches acknowledged claim succession.

This prevents the judged protocol from being implemented twice against divergent
contracts.

### Add a generic judge primitive

Add `packages/agent-loop/src/judgment/agent-judge.ts`:

```ts
interface AgentJudgeOptions<V> {
  scope: string
  bundle: WrkfJudgmentBundle
  schema: JsonSchema
  timeoutMs?: number
  tags?: string[]
}

async function agentJudge<V>(options: AgentJudgeOptions<V>): Promise<AgentResult<V>>
```

Behavior:

- one fresh `agent()` call, never `session()`;
- `output: "json"`, strict schema, `permissions: {mode: "deny"}`;
- explicit 5-minute default timeout;
- stable system framing around untrusted bundle data;
- returned runtime/trace provenance available to the caller;
- no WRKF mutation inside the primitive.

Add `packages/agent-loop/src/work/judged-action.ts` as a reusable composition over the
canonical client:

```text
claim work -> run producer -> submit -> claim judge -> agentJudge -> settle judge
```

Expose lower-level steps as well as `withJudgedAction` so production cranks can retain
typed halts and custom recovery. Its options must take separate `workIdentity` and
`judgeIdentity` values (`agentRef` plus `scopeRef`) and one stable `runnerId`; it must
never reuse the runner/coordinator principal as the claimed worker or judge. The
runner binds the returned judge runtime id through the existing `externalRunRef`
surface and supplies `expectedBundleHash` from `resolution.show`, outside the model
reply. Heartbeats wrap the producer and judge awaits when a second
dispatcher/campaign reader exists; expiry semantics remain unchanged.

Do not use `adversarialVerify` or tournament aggregation in v1. A later policy can run
multiple judge attempts and add a deterministic aggregator, but the first feature must
make one verdict lineage correct before multiplying it.

## `loops/wrkf-task-loop` and crank changes

### Five participants, with separate claim identities

The runtime becomes:

| Role | Canonical scope | Function |
|---|---|---|
| coordinator | `room-coordinator@<project>:<task>/coordinator` | Owns the crank process, recovery, suspension, and escalation. |
| tester | `room-tester@<project>:<task>/tester` | Produces the judged `test` submission and directly settles `test_fix`. |
| implementer | `room-implementer@<project>:<task>/implementer` | Directly settles `implement` with measured source facts. |
| observer | `room-observer@<project>:<task>/observer` | Produces the judged installed-surface `verify` submission. |
| judge | `room-judge@<project>:<task>/judge` | Fresh tool-denied adjudication of `test` and `verify` bundles. |

The coordinator is no longer a template action role in `@6`; it remains the live
attendant and recovery authority. The judge is not a warm room: every verdict is a
fresh invocation so prior judgments do not contaminate the next case.

Today `CrankOptions.agentRef/scopeRef` are used to claim every seat as the coordinator,
even though the runtime turn belongs to tester, implementer, or observer. Fix that as
part of this feature. Add an explicit identity to each registry entry:

```ts
interface ClaimIdentity {
  agentRef: string
  scopeRef: string
}

interface Seat {
  name: 'tester' | 'implementer' | 'observer'
  identity: ClaimIdentity
  // existing contracts/run/dispose surface
}
```

`claimCandidate` receives the selected seat's identity. Judge candidates use the
configured `room-judge` identity. `runnerId` remains the coordinator/crank process id
for every claim; it is operational provenance, not the evidence-producing principal.
The harness and dispatch configuration must require these explicit role bindings and
must not derive a principal by parsing a scope string. Direct system handlers retain
their configured mechanical principal.

Add `submitted` to the crank's terminal predecessor-status set. The engine will not
project the work candidate while its request remains pending; after an explicit
request cancellation, auto-naming that submitted predecessor preserves the existing
settled-predecessor rule while keeping the redo visible in run succession.

### Only judged seats return submissions

Do not churn the already-direct implement/test-fix contracts in the first burn-in.
Use a discriminated seat result:

```ts
type SeatExecution =
  | {
      kind: 'submission'
      proposedOutcome: string
      observations: Record<string, unknown>
      artifactRefs: Array<{ ref: string; contentHash: string; mediaType: string }>
      handoff?: string
    }
  | {
      kind: 'settlement'
      facts: Facts
      handoff?: string
    }
```

`test@6` and `verify@6` return `submission`; `test-fix@6` and `implement@6` keep the
current direct facts-shaped settlement. The judged seat submission still merges three
authorships explicitly:

1. agent assertions: proposed outcome, rationale, and observed behavior;
2. seat mechanics: commits, hashes, paths, exit codes, cleanliness, and artifact
   hashes;
3. crank linkage: exact source identity and source evidence id.

For judgment to be meaningful, enrich the two judged submissions:

- tester: test diff artifact, commands, exit codes, red failure summary, changed
  paths, commit, and cleanliness facts;
- observer: installed-surface commands, exit codes, observed behavior, verified head,
  exact implement source/linkage, and screenshot/log artifacts when applicable.

The implementer continues to write the direct `implement_result` record with its
range/diff, validation, commit, clean-tree, blocker, and test-defect facts. That record
is prior evidence in the verify bundle, not judge-authored data. Landing remains direct
and mechanical; no LLM judge is used for a plain push.

### Crank algorithm

```ts
const settledSubjects = new Set<string>()
let units = 0

while (units < maxUnits) {
  const instance = showInstance()
  haltIfClosedSuspendedWaitingOrInvalid(instance)

  const [candidate] = assertExactlyOne(await actionNext())
  const kind = candidate.executionKind
  const subject = candidate.action
  if (settledSubjects.has(subject)) halt('semantic_cycle_detected')

  if (kind === 'work') {
    const handler = resolveHandler(candidate)
    const identity = handler.kind === 'seat'
      ? handler.seat.identity
      : configuredMechanicalIdentity(handler)
    const binding = await claimWithPredecessorLaw(candidate, identity, runnerId)
    const output = await runSeatOrMechanicalHandler(binding, handler)

    if (candidate.resolution?.mode === 'agent_judge') {
      await actionSubmit(binding, output.submission)
      // This is the same semantic action continuing into judgment, not a cycle.
      // Do not mark subject settled yet.
    } else {
      await actionSettle(binding, output.settlement)
      settledSubjects.add(subject)
    }
  } else {
    if (candidate.resolution.snapshotStatus === 'stale') {
      halt('resolution_cancel_required') // no claim, artifact read, or model call
    }
    const binding = await claimWithPredecessorLaw(candidate, judgeIdentity, runnerId)
    if (binding.run.resolutionRecoveryOnly === true) {
      halt('resolution_cancel_required') // drift raced action.next; no model call
    }
    const bundle = await resolutionShow(candidate.resolution.requestId)
    assertTrustedHash(candidate.resolution.bundleHash, bundle.bundleHash)
    verifyBundleArtifacts(bundle)
    const judged = await agentJudge({scope: judgeIdentity.scopeRef, bundle, schema})
    await bindExternalRun(binding.run.id, judged.runtimeId)
    await actionSettle(binding, {
      result: 'completed',
      expectedBundleHash: bundle.bundleHash,
      verdict: judged.output,
    })
    settledSubjects.add(subject)
  }

  units += 1
}

halt('crank_budget_exhausted')
```

Add typed halts:

- `resolution_bundle_invalid`
- `resolution_artifact_unavailable`
- `judge_operational_failure`
- `judge_predecessor_unsettled`
- `resolution_stale`
- `resolution_cancel_required`
- `crank_budget_exhausted`
- `semantic_cycle_detected`

The crank still makes no judgment. It performs table lookups, validates hashes and
schema, and routes typed results. The judge makes the semantic decision; the
coordinator decides recovery after typed halts. Invalid model output and runtime
failure first settle the owned judge attempt as `operational_failed`, leaving the
request pending, and then return the typed halt. `snapshotStatus="stale"` and
`resolutionRecoveryOnly=true` are hard no-model branches; the latter is handed back
with the exact attempt-cancel/request-cancel recovery path.

### Cost and cycle guard

The existing crank can traverse multiple actions in one invocation. Removing the
manual `test_review`/`gate` halts makes a rejection loop possible. Add two mechanical
guards:

- `maxUnits` per crank invocation (default 8; explicit CLI override with a hard cap);
- a set of settled semantic action subjects. A judged work submission followed by its
  judge settlement is one permitted continuation: mark the subject only after the
  judge settles. If a later transition returns to any already settled subject, halt
  before re-claiming it.

This is ephemeral cost control, not durable loop state. A coordinator that reviews the
ledger may intentionally invoke the crank again.

### Agent source and charter amendments

Add the new agent under the source-controlled agent home
`/Users/lherron/praesidium/var/agents/room-judge`, with a narrow judgment prompt and no
mutation tools, then sync the generated Codex/runtime overlays through the established
overlay lifecycle. Amend `loops/wrkf-task-loop/CHARTER.md` from four-in-a-box to this
five-participant protocol and update
`/Users/lherron/praesidium/var/agents/room-coordinator/DISPATCH.md`: the coordinator no
longer personally claims `test_review` or `gate`, but it still owns every recovery,
suspension, cycle, stale-request, and operational-failure halt.

## `wrkq-simple-task@6`

Do not edit `@5` in place. `@6` opts into judged resolution and keeps `@5` available
for current burn-in and compatibility.

Target phases:

```text
test -> implement -> verify -> land -> done
          |            |
          +-> test_fix-+

land --push_rejected--> verify
```

Changes from `@5`:

- add role `judge` and remove coordinator-owned executable actions;
- remove executable coordinator actions `test_review` and `gate`;
- remove phases `test_review` and `gate`;
- mark only `test` and `verify` as `agent_judge` for the first burn-in;
- keep `implement` and `test_fix` direct, with their current result meanings and
  transition topology;
- keep `landing` direct/system/mechanical;
- split every judged action's submission evidence from its final result evidence;
- make the `test_complete` and `verify_complete` transitions judge-authored, with
  explicit one-to-one outcomes and separation of duty from their submission kinds;
- keep exact `source_identity` binding from implement through verify and landing;
- keep suspension outcomes for `operator_required`;
- keep direct landing and task completion projection, but route `push_rejected` back
  to `verify` because the removed `gate` phase is no longer a legal destination.

Judgment policies:

| Subject | Judge decides | Forward outcome | Retry/rewind | Escape |
|---|---|---|---|---|
| test | Tests are task-scoped, meaningful, and establish the intended red bar. | `pass -> implement` | `fail -> test` | `operator_required -> suspend` |
| verify | Exact submitted source passes the specification, installed-surface checks, and final range review. | `pass -> land` | `failed -> implement` | `operator_required -> suspend` |

The verify judge absorbs the semantic purpose of today's gate. The observer remains a
separate evidence-producing agent and cannot grade its own observations into workflow
truth. In `@6`, normalize the verify escape to the single explicit
`operator_required` outcome; judged transitions cannot retain the current v5
`otherwise` arm. `@5` remains byte-for-byte and behaviorally unchanged.

## Failure and recovery matrix

| Failure | Durable state | Automatic behavior | Coordinator path |
|---|---|---|---|
| Worker crashes before submit | Active/expired work run; no request | Nothing at expiry | Review predecessor, claim with prior run, recover or suspend. |
| Task/spec, source binding, or same-version template changes after the work claim but before submit | Active work run with the server-recorded old candidate snapshot | `action.submit` returns non-retryable `WRKF_RESOLUTION_STALE`; no request or transition is written | Attempt-only cancel/terminalize, then consciously re-claim against the new snapshot. |
| Submit commits but its response is lost | Work run `submitted`; one pending request | Exact `action.submit` replay returns the original evidence/request | Resume from the judge candidate; never rerun worker side effects. |
| Worker process dies after successful submit | Work run `submitted`; pending request | Judge candidate remains next | Re-run crank; do not repeat worker side effects. |
| Judge times out or returns invalid JSON | Judge run operationally failed; request pending | Next judge claim requires predecessor ack | Retry judge or cancel request. |
| Judge crashes after verdict but before settle | Pending request; judge run eventually contestable | No semantic change | Supersede and re-judge; tool-denied replay is safe. |
| Bundle/artifact hash mismatch | Pending request; no judge verdict | Typed halt | Repair artifact availability or cancel and redo work. |
| Task/spec, source binding, or same-version template changes after submit, with no active judge | Pending request projects `snapshotStatus=stale` | Crank makes no model call; ordinary judge claim is stale | Cancel request directly, then consciously re-claim work. |
| Drift occurs after judge claim and its active/expired token is lost | Stale pending request plus active judge authority | Request cancel refuses; a claim naming that predecessor succeeds only as recovery-only and atomically supersedes/revokes it | Confirm completed settle is stale, settle recovery attempt `cancelled`, cancel request, then consciously re-claim work; invoke no model. |
| Verdict cites known evidence outside the persisted bundle, including another instance | Pending request and active judge run remain unchanged | `WRKF_VERDICT_INVALID`; no evidence/transition/effect/token write | Correct the runner/model contract or operationally fail the attempt and retry. |
| Request cancel attempted while a current judge run is active or merely expired | Active judge run and pending request remain unchanged | Cancel refuses; expiry never revokes authority | Claim a normal successor naming `priorRun`, settle that attempt `cancelled`, then cancel the request with canonical principal and reason. |
| Judge selects retry outcome | Atomic transition back to prior phase | Crank halts before repeated subject | Coordinator reviews, then re-cranks deliberately. |
| Judge selects operator-required outcome | Instance suspended in current phase | Crank returns suspension halt | Fix-and-resume or escalate under current doctrine. |
| Two judge runners race | One claim wins | Loser receives lease conflict | Read predecessor/current run; no duplicate settlement. |
| Superseded judge settles late | Refused with successor id | None | Judge learns eviction in-band. |
| Judge settlement commits but response or effect delivery is lost | Request/run/evidence/transition/effect obligations are already committed once | Exact settle replay returns the committed result; delivery resumes idempotently | Read back before retrying any judge; never call the model again. |
| Identical settle replay | Original result returned | Idempotent | None. |
| Conflicting settle replay | Refused | None | Inspect ledger; never choose latest evidence. |

## Rollout plan

Implement in reviewable slices, in this order:

1. **Proposed doctrine record only (wrkq).** Create
   `wrkq.wrkf-action.judged-resolution` with `status: proposed` (plus an ADR/provenance
   link if useful) and run `just architecture-records`. It must remain absent from
   normative projections. Do **not** yet amend the active
   `wrkq.wrkf-engine-runner-contract-fixtures` or `wrkq.contract.wrkf-rpc` records;
   aspiration must not outrank the live producer.
2. **Template model and storage (wrkq).** Add `resolution` validation,
   `workflow_resolution_requests`, run kind/linkage, bundle hashing, and ledger reads.
3. **Submit and judged-settle engine path (wrkq).** Add `action.submit`, judge candidate
   projection/claim, judge settlement, cancellation, atomicity, replay, suspension,
   stale, and succession tests.
4. **RPC/CLI/client surface (wrkq).** Add protocol DTOs/methods, CLI commands/readback,
   TypeScript client facade, schema hash update, and cross-language fixtures.
5. **Agent-loop contract cleanup.** Remove stale local DTO mirrors/workspace leases,
   align claim succession and source identity, and add the generic tool-denied judge
   primitive.
6. **`wrkq-simple-task@6`.** Judge only `test` and `verify`, remove `test_review` and
   `gate`, keep `implement`/`test_fix` direct, return landing `push_rejected` to
   `verify`, add source/carry/SoD contracts and behavior tests, and preserve `@5`
   unchanged.
7. **Crank and agent integration.** Fix seat-principal claims, add the source-controlled
   `room-judge`, add submission-producing test/verify seats, judge routing,
   `externalRunRef` binding, typed halts, budget/cycle guards, CLI/readback, charter,
   source-agent overlay, and coordinator-dispatch amendments.
8. **Real installed E2E and burn-in.** Install both repos and drive a real task through
   worker submission, fresh judge invocation, judged settlement, exact-source verify,
   landing, and durable readback.
9. **Guarded law activation.** Only after the engine, RPC, client, canonical fixture,
   agent-loop integration, and real evidence exist, promote
   `wrkq.wrkf-action.judged-resolution` to `active` and amend
   `wrkq.wrkf-engine-runner-contract-fixtures` plus `wrkq.contract.wrkf-rpc` in the
   same guarded landing. Populate real `source`, `required_tests`, and fresh
   `last_verified` evidence, regenerate normative projections, and pass
   `just architecture-records` plus `just verify`. Keep
   `wrkq.wrkf-action.lease-recovery` and
   `wrkq.simple-task.v1.naive-supersede` unchanged; the new local stale fence does not
   claim global template immutability.

Do not split engine and client changes such that a new server can emit judge candidates
that the installed client cannot discriminate.

## Verification plan

### WRKF focused tests

- template validation for every invalid resolution shape, especially `otherwise`, a
  non-judge transition producer, missing SoD, and outcome/result mismatch;
- direct action behavior and the complete `wrkq-simple-task@5` fixture unchanged;
- bypass boundary tests proving generic `evidence.add` cannot create the judged final
  kind, a judged work run cannot complete/settle its transition, `action.complete`
  cannot do so, and public `transition.apply` cannot apply the judged subject
  transition before or after submit;
- submit atomicity, schema validation, token revocation, and exact idempotent replay,
  including commit-after-response-loss adoption;
- claim -> task edit -> submit refusal against the server-recorded candidate snapshot;
- claim -> same-version template-definition replacement -> submit returns stale with
  no submission evidence, request, run terminalization, effect, or token mutation;
- `UNIQUE(subject_run_id)` and concurrent partial-unique pending-request enforcement;
- judge candidate priority, `action=<subjectAction>`, `executionKind=judge`, and
  deterministic persisted canonical bundle bytes/hash;
- existing separation-of-duty rejection at judge claim and again at settle;
- carry facts copied from submission and impossible to override;
- exact source identity through direct implement -> judged verify -> direct landing;
- trusted-runner wrong bundle hash, missing citation, unknown outcome, and stale
  task/revision rejection, all before transition mutation;
- submit -> same-version template-definition replacement -> first judge claim returns
  stale with no run/token write;
- submit -> claim judge -> same-version template-definition replacement -> completed
  settle returns stale with no evidence, transition, request, run, effect, obligation,
  or token mutation;
- an out-of-bundle known citation and a cross-instance citation each return
  `WRKF_VERDICT_INVALID` with no evidence, transition, request, run, effect,
  obligation, or token mutation;
- judge operational failure leaves request pending;
- judge claim succession, late settle, superseded settle, and conflicting replay;
- resolution cancellation refusal while an active/expired judge exists, followed by
  successor claim -> attempt cancellation -> request cancellation -> conscious worker
  re-claim;
- submit -> claim judge -> task/template/source drift -> lost active/expired token ->
  cancel refusal -> successor claim naming the predecessor creates a recovery-only run
  and atomically supersedes/revokes the predecessor -> completed settle is stale ->
  recovery attempt cancellation -> request cancellation -> conscious work re-claim,
  with ledger lineage and zero model/verdict/transition/effect/obligation activity;
- recovery-only run refuses bindExternal, fail, complete, evidence writes, and every
  settlement result except `cancelled`;
- suspension outcome and suspended-write gate;
- atomic rollback injection at final evidence, transition/suspension, durable
  effects/obligations, request, run, and token cleanup;
- crash-after-commit/before-response and crash-after-commit/before-effect-delivery
  replay without a second verdict or transition;
- `@6` landing `push_rejected -> verify` re-entry and exact-source rebinding.

### Client and contract tests

- Go/RPC/TypeScript DTO parity for direct work, judged work, and judge candidate union
  arms plus submission, resolution, and verdict; every candidate arm retains
  `transition`, source, handler, workspace mode/ref, side-effect classes, rank,
  blocked, and blocked-reason compatibility fields;
- protocol catalog/schema hash coverage;
- engine-runner fixtures for direct-work -> settle and judged-work -> submit ->
  judge-candidate -> settle, proving the generic runner selects only from the
  discriminants and never from template-specific action names;
- negative fixture proving run id cannot substitute for exact source identity;
- old/direct template behavior remains compatible; its candidate gains only the
  required `executionKind="work"` and `templateDefinitionHash` authority fields, while
  its claim/settle semantics remain unchanged and an old direct consumer ignores the
  additive fields successfully;
- pre-implementation `just architecture-records` passes with
  `wrkq.wrkf-action.judged-resolution` still `proposed`, absent from normative
  projections, and both active contracts unchanged;
- guarded implementation landing promotes the invariant and amends the runner/RPC
  contracts only after every named source/test exists with fresh evidence; regenerated
  projections pass `just architecture-records` and `just verify`, while lease recovery
  and the accepted same-version supersede risk remain unchanged.

### Agent-loop tests

- strict judge schema and tool-denied invocation options;
- untrusted-evidence prompt framing;
- fresh invocation per verdict (no continuation/session reuse);
- seat work claims use tester/implementer/observer canonical principals and scopes,
  judge claims use `room-judge`, and `runnerId` remains the coordinator/crank;
- artifact hash verification and deterministic truncation;
- operational failure versus semantic operator-required distinction;
- stale request detection before any model call;
- stale candidate and recovery-only binding paths invoke neither `agent()` nor
  artifact materialization and return the exact cancellation guidance;
- typed crank halts and budget/cycle protection, including permitted work -> judge
  continuation for one request and a halt only after judged settlement returns to an
  already settled subject;
- no worker self-settlement for judged actions;
- direct versus submit routing comes solely from the candidate union, with no
  hard-coded `test`/`verify` branch in the generic crank;
- no worker side-effect replay after a durable submission or lost submit response;
- actual judge runtime bound through the existing run `externalRunRef`;
- only test/verify use submission/judge routing; implement/test-fix stay direct;
- landing `push_rejected` returns to verify rather than a removed gate;
- current claim succession/predecessor behavior through the canonical client;
- `submitted` is treated as a terminal predecessor status for work attempts, while the
  associated resolution request remains the executable semantic continuation.

### Required real E2E

Use installed `wrkq`/`wrkf` and installed `agent-loop`, a real `room-*` worker turn, and
a real `room-judge` turn. Exercise:

```text
action.next(work)
  -> action.claim(priorRun:null)
  -> real worker/observer turn
  -> action.submit
  -> action.next(judge)
  -> action.claim(priorRun:null)
  -> real tool-denied judge turn
  -> action.settle(verdict)
  -> durable evidence/instance/task readback
```

The negative control must attempt to settle with an unrelated/latest evidence record
or the correct run id but wrong source/bundle hash and observe refusal. A second
recovery control must crash/stop after submit, restart the crank, and prove the worker
side effects are not re-executed. A third control must lose the judge-settle response
after commit and prove replay returns the original settlement and resumes delivery
without calling the judge again. Finally force `landing_result=push_rejected` and prove
the installed `@6` instance re-enters `verify`, never the removed `gate` phase.

Completion gate:

- focused tests;
- `scripts/agent-check.sh` and `just verify` in wrkq;
- `just verify` in agent-loop;
- `just install` in both repos;
- installed CLI/client smoke;
- real agent-loop worker + judge E2E;
- final `wrkq`, `wrkf action`, `wrkf evidence`, `wrkf resolution`, timeline, git, and
  artifact readback.

## Alternatives considered

### Keep explicit review phases only

This needs no engine work and is the current v5 pattern. It is durable, but each judged
boundary becomes another template phase/action, review logic remains bespoke, and the
crank must hand control back for manual coordinator settlement. It does not provide a
reusable evidence/submission/judge contract.

### Call an LLM in memory before today's settle

This is easy to prototype but loses the submission on crash, cannot prevent bypass,
cannot independently retry/supersede judges, and leaves the ledger unable to explain
what exact bundle was judged. Reject for production.

### Let WRKF invoke the LLM

This couples the local ledger to runtime/model availability, puts prompts and tool
policy in the engine, and violates the ratified deterministic-engine boundary. Reject.

### Let the judge call WRKF tools directly

This exposes owner authority to untrusted model output and turns prompt injection into
a state-mutation path. Reject. The trusted runner is the only caller of submit/settle.

### Multi-judge quorum in v1

Potentially useful for high-risk policies, and agent-loop already has adversarial and
tournament primitives. It multiplies cost and introduces aggregation semantics before
single-verdict lineage is proven. Defer until one-judge burn-in produces calibration
data.

## Non-goals and deferred work

- WRKF does not choose models, store full prompts, or manage prompt bundles.
- No natural-language parsing inside the engine.
- No LLM judgment for deterministic landing/push success.
- No global campaign scheduler or parallel task fanout.
- No cryptographic identity/authentication claim beyond the current local principal
  model.
- No confidence thresholds or majority voting in v1.
- No automatic cancellation of stale requests or automatic retry of semantic
  rejections.
- No mutation of `wrkq-simple-task@5`; `@6` is explicit.

## Acceptance decision

Proceed if the team agrees on these three lines:

1. **WRKF supports judgment as a durable protocol but never runs the judge.**
2. **A worker submission and a judge verdict are separate evidence records, attempts,
   identities, and leases.**
3. **The crank may automate the protocol, but exact-source mechanical gates and atomic
   settlement remain stronger than any LLM verdict.**
