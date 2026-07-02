# WRKF Samples

Status: non-normative implementation examples

These samples illustrate the WRKF lean/greenfield model. The specifications are normative; this file shows concrete payloads and flows that an implementation should be able to support.

## 1. Agent notification: fenced run binding for implementation

This is the kind of object a runner may include in an agent turn or runtime notification after WRKF has claimed an `implement` run attempt. It is a concrete execution binding, not workflow truth by itself.

```json
{
  "type": "wrkf.run_binding",
  "recipient": "agent:cody",
  "scopeRef": "agent:cody:project:wrkq",
  "runnerId": "agent-loop-wrkf-runner:r1",
  "task": {
    "taskUuid": "task_abc",
    "taskRef": "T-05090",
    "projectRef": "wrkq",
    "path": "wrkq/inbox/fix-runner-orphan-recovery",
    "title": "Fix runner orphan recovery"
  },
  "workflow": {
    "instanceId": "wf_i_123",
    "templateId": "simple-task",
    "templateVersion": "1",
    "state": {
      "status": "active",
      "phase": "ready"
    },
    "expectedStateRevision": 2,
    "expectedTaskDocHash": "taskdoc:8a91f2"
  },
  "run": {
    "runId": "wr_run_impl_002",
    "semanticActionKey": "implement:wf_i_123:r2",
    "attempt": 1,
    "action": "implement",
    "role": "implementer",
    "transition": "implement_complete",
    "resultEvidenceKind": "implement_result",
    "inputHash": "input:7a3c0e"
  },
  "handler": {
    "contract": "praesidium.simple-task.implement@1",
    "workspaceMode": "exclusive",
    "sideEffectClasses": ["worktree.write", "git.commit"]
  },
  "workspace": {
    "workspaceRef": "workspace:wrkq:/repo/wrkq",
    "repoPath": "/repo/wrkq",
    "baseSha": "base0001"
  },
  "authority": {
    "ownerToken": "own_4d5c2f0b9f7a4d6c",
    "ownerGeneration": 4,
    "leaseExpiresAt": "2026-06-30T20:30:00Z"
  },
  "settlementContract": {
    "success": {
      "result": "completed",
      "transition": "implement_complete",
      "requiredEvidence": {
        "kind": "implement_result",
        "facts": {
          "result": "done",
          "commit.sha": "<HEAD>",
          "git.clean": true,
          "base.sha": "base0001",
          "postcondition": "git_committed_clean"
        }
      }
    },
    "operatorEject": {
      "result": "operator_required",
      "transition": "implement_operator_required",
      "evidenceKind": "operator_required",
      "reason": "git_postcondition_failed"
    }
  },
  "instructions": {
    "summary": "Implement the task, commit the changes, and leave the repository clean.",
    "mechanicalPostcondition": {
      "check": "git_committed_clean",
      "maxRepairTurns": 1,
      "onFirstFailure": "The runner will send one corrective turn asking you only to stage/commit the existing intended changes and leave git clean.",
      "onSecondFailure": "The same WRKF run is settled operator_required and ejected to the operator."
    }
  }
}
```

## 2. Triage -> implement -> verify -> done lifecycle

Assume:

```text
task:       T-05090 / task_abc
project:    wrkq
instance:   wf_i_123
runner:     agent-loop-wrkf-runner:r1
triager:    agent:cody, scopeRef agent:cody:project:wrkq
implementer:agent:cody, scopeRef agent:cody:project:wrkq
verifier:   agent:smokey, scopeRef agent:smokey:project:wrkq
```

### 2.1 Attach

```ts
WorkflowInstance {
  id: "wf_i_123",
  task: { taskUuid: "task_abc", taskRef: "T-05090", projectRef: "wrkq" },
  template: { id: "simple-task", version: "1" },
  state: { status: "active", phase: "needs_triage" },
  stateRevision: 1
}
```

### 2.2 Triage

Candidate:

```text
semanticActionKey = triage:wf_i_123:r1
action = triage
role = triager
```

Claim creates run:

```text
runId = wr_run_triage_001
agentRef = agent:cody
scopeRef = agent:cody:project:wrkq
```

Triage may execute locally with no HRC run.

Settlement:

```json
{
  "runId": "wr_run_triage_001",
  "result": "completed",
  "evidence": {
    "kind": "triage_result",
    "facts": { "executable": true, "risk": "medium" },
    "data": { "summary": "Task is actionable." }
  },
  "transition": "triage_complete"
}
```

State becomes:

```text
active/ready, revision 2
```

### 2.3 Implement

Candidate:

```text
semanticActionKey = implement:wf_i_123:r2
action = implement
role = implementer
workspaceRef = workspace:wrkq:/repo/wrkq
```

Claim creates run:

```text
runId = wr_run_impl_002
agentRef = agent:cody
scopeRef = agent:cody:project:wrkq
```

Runner launches HRC for the long-running implementation session:

```text
externalRunRef = hrc:hrc_run_7002
```

Successful settlement after runner-backed `git_committed_clean`:

```json
{
  "runId": "wr_run_impl_002",
  "result": "completed",
  "evidence": {
    "kind": "implement_result",
    "facts": {
      "result": "done",
      "commit.sha": "abc1234",
      "git.clean": true,
      "base.sha": "base0001",
      "postcondition": "git_committed_clean",
      "repair.turns": 0,
      "externalRunRef": "hrc:hrc_run_7002"
    },
    "data": { "summary": "Implemented fix and committed changes." }
  },
  "transition": "implement_complete"
}
```

State becomes:

```text
active/implemented, revision 3
```

WRKF derives mandatory verify:

```text
semanticActionKey = verify:wf_i_123:wr_run_impl_002:abc1234
sourceRunId = wr_run_impl_002
commitSha = abc1234
```

### 2.4 Verify

Candidate:

```text
semanticActionKey = verify:wf_i_123:wr_run_impl_002:abc1234
action = verify
role = tester
source = wr_run_impl_002 / abc1234
```

Claim creates run:

```text
runId = wr_run_verify_003
agentRef = agent:smokey
scopeRef = agent:smokey:project:wrkq
externalRunRef = hrc:hrc_run_7003
```

Settlement:

```json
{
  "runId": "wr_run_verify_003",
  "result": "completed",
  "evidence": {
    "kind": "verify_result",
    "facts": {
      "result": "passed",
      "sourceRunId": "wr_run_impl_002",
      "verifiedCommit": "abc1234",
      "externalRunRef": "hrc:hrc_run_7003"
    },
    "data": { "summary": "All verification checks passed." }
  },
  "transition": "verify_complete"
}
```

State becomes:

```text
closed/done, revision 4
```

## 3. Runner-backed git commit postcondition and settlement validation

The MVP uses runner-backed mechanical validation. The runner checks git state and submits facts. WRKF validates the facts against the template and transition guards; it does not run git commands during settlement in the MVP.

### 3.1 Template excerpt

```yaml
id: simple-task
version: 1

states:
  - { status: active, phase: ready }
  - { status: active, phase: implemented }
  - { status: waiting, phase: operator_required }

evidenceKinds:
  - kind: implement_result
    requiredFacts:
      - result
      - commit.sha
      - git.clean
      - base.sha
      - postcondition
    factSchema:
      type: object
      required:
        - result
        - commit.sha
        - git.clean
        - base.sha
        - postcondition
      properties:
        result: { const: done }
        commit.sha:
          type: string
          pattern: "^[0-9a-f]{7,40}$"
        git.clean: { const: true }
        base.sha:
          type: string
          pattern: "^[0-9a-f]{7,40}$"
        postcondition: { const: git_committed_clean }
        repair.turns:
          type: integer
          minimum: 0
          maximum: 1

  - kind: operator_required
    requiredFacts:
      - reason

actions:
  - id: implement
    when: { status: active, phase: ready }
    role: implementer
    transition: implement_complete
    resultEvidenceKind: implement_result
    workspaceMode: exclusive
    sideEffectClasses:
      - worktree.write
      - git.commit
    continuation:
      afterAction: implement
      nextAction: verify
      attentionScope: workspace
      releaseOn: [completed, semantic_blocked, operator_required, cancelled]

transitions:
  - id: implement_complete
    from: { status: active, phase: ready }
    to: { status: active, phase: implemented }
    role: implementer
    requiresEvidenceKind: implement_result
    guardFacts:
      result: done
      git.clean: true
      postcondition: git_committed_clean

  - id: implement_operator_required
    from: { status: active, phase: ready }
    to: { status: waiting, phase: operator_required }
    role: implementer
    requiresEvidenceKind: operator_required
    guardFacts:
      reason: git_postcondition_failed
```

### 3.2 Passing settlement

```json
{
  "runId": "wr_run_impl_002",
  "ownerToken": "own_4d5c2f0b9f7a4d6c",
  "ownerGeneration": 4,
  "result": "completed",
  "evidence": {
    "kind": "implement_result",
    "facts": {
      "result": "done",
      "commit.sha": "abc1234",
      "git.clean": true,
      "base.sha": "base0001",
      "postcondition": "git_committed_clean",
      "repair.turns": 1,
      "externalRunRef": "hrc:hrc_run_7002"
    },
    "data": {
      "summary": "Implemented fix and committed changes after one corrective turn."
    }
  },
  "transition": "implement_complete"
}
```

WRKF accepts because ownership is current, the run is active, the instance is still in `active/ready`, the evidence kind is `implement_result`, required facts are present, the schema passes, and the transition guard facts match.

### 3.3 Rejected completed settlement

```json
{
  "runId": "wr_run_impl_002",
  "ownerToken": "own_4d5c2f0b9f7a4d6c",
  "ownerGeneration": 4,
  "result": "completed",
  "evidence": {
    "kind": "implement_result",
    "facts": {
      "result": "done",
      "git.clean": false,
      "base.sha": "base0001"
    }
  },
  "transition": "implement_complete"
}
```

WRKF rejects because `commit.sha` is missing, `git.clean` is false, `postcondition` is missing, the evidence schema fails, and the transition guard facts do not match.

### 3.4 Operator-required settlement after second failure

```json
{
  "runId": "wr_run_impl_002",
  "ownerToken": "own_4d5c2f0b9f7a4d6c",
  "ownerGeneration": 4,
  "result": "operator_required",
  "evidence": {
    "kind": "operator_required",
    "facts": {
      "reason": "git_postcondition_failed",
      "postcondition": "git_committed_clean",
      "attempts": 2,
      "git.clean": false,
      "base.sha": "base0001",
      "head.sha": "abc1234",
      "externalRunRef": "hrc:hrc_run_7002"
    },
    "data": {
      "summary": "Cody failed to leave a clean committed worktree after one corrective turn.",
      "operatorAction": "Inspect workspace; commit, revert, or cancel."
    }
  },
  "transition": "implement_operator_required"
}
```

WRKF accepts this if the template allows `implement_operator_required` from `active/ready` with `operator_required` evidence and `reason=git_postcondition_failed`.
