# WRKF Simplification — v5 Engine Scope

**Status:** ratified design, 2026-07-12 (Lance + mable@agent-loop; engine feedback from cody@wrkq:primary assessed and incorporated selectively; claim-succession contract and workspace-lease purge ratified same day, superseding the earlier reaper section).
**Where:** the wrkq repo, wrkf engine only.
**Stance:** forward-fix, breaking. Old shapes die; nothing is migrated, mirrored, or kept compatible.
**Untouched:** wrkq-leaf-drain (dead code), taskboard, the `wrkq-simple-task@4` template, any existing loop. The new agent loop and the `@5` template are written from scratch *after* this engine work, against the engine this scope produces.

---

## Why

`wrkq-simple-task@4` doubled from 29KB to 43KB in one commit because operator parks are modeled as state transitions. The instance state is flat `{status, phase, outcome}` — no memory of where a task parked from — so the template hand-unrolls eight `operator_resolved_from_<phase>` blocks, each duplicating resume/close/cancel outcomes with full effects. ~200 lines of pure cross-product duplication that drifts and grows with every new phase.

Two fixes, one principle. **Parking stops being a place and becomes a condition**: a parked task stays in its phase and gains a suspension; remove the suspension and it is back where it was. And **the engine stops interpreting**: it records what happened and reports it at the point of decision; what an event *means* — retry, park, abandon — is the caller's judgment, always.

## What gets built

### 1. Suspension as a first-class condition on a workflow instance

A running instance can gain exactly one suspension: a small record holding a unique **suspension id**, a template-declared **reason code**, a **timestamp**, and a **pointer to what caused it**. The instance's status/phase/outcome do not change — a parked task is still *in* its phase, just stopped. Suspending an already-suspended instance is rejected. There is no stack, no history structure, no park-state in the graph.

### 2. One write gate while suspended

At each point where the engine writes to an instance — `TransitionForSelectors` (ledger.go), `applyActionTransitionTx` (action.go), and `ClaimAction` before it issues new run authority — the same active-suspension fact rejects the write with `WRKF_SUSPENDED`. Reads and inspection work normally. A worker who claimed before the park bounces when it tries to settle; a new worker cannot create a dead-on-arrival claim while parked. The claim refusal carries only the active suspension record and takes precedence over predecessor succession review. No token invalidation, no authority model, no admission matrix.

### 3. Template DSL: suspend as an outcome, declared once

Any transition outcome may say `suspend: {reason: X}` **instead of** `to: Y` — exactly one of the two, validated at template load (tagged union in Go: `To *State` XOR `Suspend *SuspendSpec`; a missing `to` must not decay to a zero-value State). Optionally, the template declares once what extra effects run on each disposition (resume / close / cancel). That is the entire template surface. The `waiting/operator_required` state pattern and per-phase resolution blocks will not exist in `@5`. Suspension has exactly one author: template-declared outcomes driven through normal transitions. The engine never opens a suspension on its own initiative; there are no engine-reserved reason codes.

### 4. One resolution command

`resolveSuspension(suspensionId, disposition)` — atomic, one transaction: load instance, check the presented suspension id matches the active suspension, apply the disposition's declared effects, clear the suspension, bump revision, emit the event.

- **The matching suspension id is the ONLY gate.** No evidence contracts, no discriminator enums, no role validation, no per-reason policy machinery.
- Dispositions: **resume** (task is back in its parked phase with everything exactly as it was; resume returns EXACTLY to the parked phase — backing up is a normal transition afterward), **close** (done), **cancel** (cancelled).
- Operator explanation is recorded free text, never validated. Ordinary revision CAS applies.

### 5. The task document is never touched by park or resume

No `blocked`/`open` task-state mirroring. The suspension on the instance is the sole truth; consumers that want to show "blocked" read it. Consequence: evidence freshness needs no rework — nothing changes under a parked task, so pre-park evidence is valid after resume with the freshness code as-is.

### 6. Two honest event types

`workflow.suspended` and `workflow.suspension_resolved`, carrying the suspension record, disposition, and revisions — admitted by event storage and queries. No fake self-transitions. `watch` gains a "suspended" predicate (today it only wakes on `status=waiting`, which no longer fires for parks). Instance DTOs / inspect output expose the suspension.

### 7. The claim succession contract

This replaces every reaper concept, eager or lazy.

**Lease semantics.** A lease is a time-boxed exclusive claim on a unit of work; its heartbeat is the worker's liveness signal. TTL expiry means exactly one thing: **the claim is contestable.** It is not death, not failure, and not an event requiring a response — the holder may be crashed, stuck, partitioned, asleep (laptop lid), finished-but-unsettled, or deliberately abandoned, and the engine cannot and does not distinguish. Nothing happens at the moment of expiry.

**The claim gate.** `claim(action, priorRun: <run-id | null>)` — CAS-shaped, like `resolveSuspension` and `ExpectRevision`. If a prior run exists for this unit of work and the caller's `priorRun` does not name it, the claim is **refused**, and the refusal carries the predecessor's full record: owner, claimed/heartbeat/expiry timestamps, exact settle status, an engine-computed settled discriminator, declared side-effect classes, external run ref, workspace ref, evidence written. Consumers use the discriminator instead of owning a terminal-status enumeration. The caller reviews, then re-claims naming the predecessor. First-ever claim: `priorRun: null`. Review is structurally unavoidable — the information and the decision point arrive in the same round-trip — but the engine never asks what the caller concluded. Interpretation is the caller's business.

**What acknowledgment does, atomically with the claim:**
1. The prior run is terminalized as **superseded** (not "failed" — the engine does not know it failed; it knows it was replaced).
2. The prior lease token is revoked: a late settle from the predecessor is refused with "superseded by <run>" — the returning worker learns the truth in-band.
3. A succession event is recorded in the ledger: who evicted whom.

**Late settle rule.** If no successor has claimed, a late settle from an expired lease is **accepted** — the work is real, the lapse was harmless. Expiry alone forfeits nothing; **only eviction forfeits**, and eviction happens only at a successor's claim. The engine never unilaterally terminalizes a run.

**Uniformity.** Retrying over a cleanly *settled* prior attempt also names it. Every retry is a conscious retry over a named predecessor; attempt lineage is a chain of acknowledgments in the ledger. No special-casing lapsed vs settled.

**Where "needs a human" comes from now.** The successor reviews the predecessor record; if it judges the situation warrants an operator (side-effectful, unconfirmable), it drives the template's suspend outcome like any other actor. Engine mechanics: report, refuse-without-ack, hand over. Loop policy: everything else. The new loop's spec inherits this doctrine line: *before claiming over a side-effectful predecessor, review; when unconfirmable, suspend.*

## What gets purged

### 8. contextHash — deleted entirely

The `contextHash()` function (service.go), the instance field, the `TransitionOptions` field, the mismatch error, and every stamp of it on events, webhooks, DTOs, and action input. It was a redundant etag — the stored value only ever changed when the revision changed, so it caught nothing `ExpectRevision` doesn't — plus an inconsistently-computed fingerprint (settlement path hashed evidence/obligations/effects; direct-transition and close paths passed nil) that nothing read back. **Revision is the sole CAS token.** Consumers detect change with `instanceId + revision`.

### 9. The reaper — deleted wholesale

The daemon sweep (`cli/daemon.go`), the CLI reap command, the RPC endpoint, `ReapActions` itself, the ambiguity classifier (the externalRunRef / workspaceRef / sideEffectClasses heuristics), and the `operator_required` run stamping. **No lazy variant either** — no dead-on-contact terminalization. Supersession-at-claim (§7) is the only path by which a run record changes hands.

### 10. The old operator-park plumbing — deleted

- The hard-coded `operator_resolution` reason validation in `policy.go`.
- The `ledger.go` supervisor-call/escalation cleanup keyed to the `operator_resolved` transition id.

### 11. Workspace lease machinery — deleted (ratified 2026-07-12)

`workspace_lease.go` wholesale: `ClaimWorkspace` / `HeartbeatWorkspace` / `ReleaseWorkspace` / `ShowWorkspace`, the lock table, token/generation matching, and the workspace replay validation in action claiming. The engine was acting as a lock server for opaque directory paths — which directory a seat runs in is dispatch policy and belongs to the loop; a single loop serializes its own checkouts trivially. **`workspaceRef` survives as an opaque reported fact on run records** (part of the predecessor record a successor reviews); the engine records it and never interprets it.

## Deferred until seen in the wild

- **Claim contention races** (two callers racing to claim over the same predecessor): the CAS already serializes them correctly; we build nothing extra unless real-world use demands it.
- **Cross-dispatcher workspace collision**: with one loop there is no contention; revisit only if a second dispatcher ever exists.

## Explicit non-goals

- No admission matrix or permission machinery — the one write gate is it.
- No evidence contracts, discriminator enums, or role validation on resolution.
- No engine-opened suspensions, no engine-reserved reason codes.
- No run-authority invalidation beyond supersession-at-claim.
- No template drift detection, reconciliation, or supersede-under-suspension handling — instances are ephemeral (attach, run the flow, close the task) and templates are static once correct. `templateHash` stays as write-once provenance that nothing checks.
- No commit-path unification refactor — all three doors get the same guard and that's all.
- No migrations, no backward compatibility, no dual-mode support.
- No changes outside the wrkq repo.

## Control-point inventory

| # | What | Kind |
|---|---|---|
| 1 | Writes to a suspended instance are rejected (one fact, three doors) | GATE |
| 2 | `resolveSuspension` must present the matching suspension id | GATE |
| 3 | `claim` must present the prior run id (`null` for a first claim) | GATE |
| 4 | Ordinary revision CAS on engine writes | HASH (existing, unchanged) |

Three CAS-shaped gates and one counter. Anything resembling a fifth control point is out of scope by ruling.

## What this buys

The `@5` template expresses "needs a human" in one outcome line and one resolution declaration instead of ~200 unrolled lines. A from-scratch agent loop gets a flat, honest engine surface — phase means phase, suspended means suspended, revision means changed, and taking over work means naming what you took it over from — with four total control points and an engine that records everything and presumes nothing.

## Tasks

Tracked in the `wrkq` project under `wrkf-v5-suspend/`:

1. **T-06260** `suspension-model-write-gate` — instance suspension record + the suspended-write guard on both commit paths.
2. **T-06261** `template-dsl-suspend-outcome` — `to` XOR `suspend` outcome union + per-disposition effects, load-time validation.
3. **T-06262** `resolve-suspension-command` — atomic `resolveSuspension` with id-only gate and three dispositions.
4. **T-06263** `suspension-events-watch-dto` — the two event types, watch predicate, DTO/inspect exposure.
5. **T-06264** `purge-context-hash` — delete contextHash everywhere; revision-only CAS.
6. **T-06265** `purge-operator-park-plumbing` — delete the reaper wholesale + policy.go/ledger.go operator-park special-casing.
7. **T-06266** `claim-succession-contract` — the `priorRun` CAS gate, supersession semantics, late-settle rule, succession event.
8. **T-06267** `purge-workspace-leases` — delete workspace lease machinery; `workspaceRef` becomes an opaque run fact.
9. **T-06299** `defect-claim-not-suspension-gated` — extend the suspended-write gate to `action.claim`, before succession evaluation.

Suggested order: the purges (T-06264, T-06265, T-06267) can land first or in parallel with T-06260; T-06261–T-06263 build on T-06260; T-06266 reshapes claim/settle and should coordinate with T-06265 (the reaper deletion) landing before or with it. The `@5` template and the new loop follow outside this scope.
