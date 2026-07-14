# wrkq-simple-task@5 — Manual Smoke Plan (wrkf CLI only)

**Ruling:** manual validation is wrkf-CLI-only, against the CANONICAL shared DB, real mode — no throwaway DBs (Lance ruling, supersedes the earlier isolated-DB note). Containment: dedicated `wrkf-v5-smoke` container, `stv5-*` slugs, archive-after-transcript. Coordination stays with mable@wrkq.
**Template under test:** `internal/workflow/builtins/wrkq-simple-task-v5.workflow.json` — validated at authoring: `valid wrkq-simple-task@5 sha256:f3f5b553ee6fb130e31ff3ae8258a03d1db646775da6c96fee26a48d70892bbd`.
**CLI surfaces referenced:** `wrkf workflow validate`, `wrkf suspension resolve SUSPENSION_ID --disposition resume|close|cancel`, `wrkf ... claim`, `wrkf watch --until suspended`.

Every case states the assert. A case without its assert observed is a FAIL — no "close enough."

---

## A. Static template validation

| # | Case | Assert |
|---|------|--------|
| A1 | `wrkf workflow validate` on the v5 file | `valid wrkq-simple-task@5` + the hash above |
| A2 | Mutate: `suspension.reasons` → `["something_else"]` | invalid: every suspend outcome reports reason not declared *(verified at authoring)* |
| A3 | Mutate: add `suspension.effects.reopen: []` | invalid: unknown disposition "reopen" *(verified at authoring)* |
| A4 | Mutate: give a suspend outcome a `to` as well | invalid: exactly one of to or suspend *(verified at authoring)* |
| A5 | Mutate: remove both `to` and `suspend` from an outcome | invalid: exactly one of to or suspend |
| A6 | Mutate: remove ALL suspend outcomes, keep `suspension` block | invalid: suspension declared but no outcome uses suspend |
| A7 | Mutate: add a suspend outcome to a transition whose `from.status` is `closed` | invalid: cannot suspend from a closed state |

## B. Park (suspend outcome)

Setup: scratch DB, install @5, create task, attach instance (initial: active/test).

| # | Case | Assert |
|---|------|--------|
| B1 | Claim the `test` action (priorRun null), settle with `test_result` `result=operator_required` | Instance gains suspension: non-empty `id`, `reason=operator_required`, `at`, `causeRef`. **Status/phase UNCHANGED: active/test.** |
| B2 | Inspect the wrkq task document after B1 | Task state UNTOUCHED (no `blocked`). Description/spec/meta byte-identical. This is the freshness invariant — hard fail if anything moved. |
| B3 | Event query after B1 | `workflow.suspended` event present, carrying the suspension record and before/after revisions. No `workflow.transitioned` self-transition. |
| B4 | Instance DTO (`workflow show`/inspect) | `suspension` object exposed; no `contextHash` field anywhere in the DTO (purge check). |

## C. Write gate while suspended

| # | Case | Assert |
|---|------|--------|
| C1 | Attempt any ordinary transition | Refused: instance suspended. |
| C2 | Attempt a new action claim | Refused. |
| C3 | Pre-park zombie: claim an action BEFORE parking (separate instance), park via another actor's path, then let the original claimer settle | Settle refused — the pre-park worker bounces off the gate. This is the fencing story; hard fail if the settle lands. |
| C4 | Reads while suspended | show/inspect/watch/event-query all work normally. |

## D. resolveSuspension (id-only gate)

| # | Case | Assert |
|---|------|--------|
| D1 | `wrkf suspension resolve WRONG_ID --disposition resume` | Refused: id mismatch. Instance untouched. |
| D2 | Resolve with empty/missing id | Refused (usage or id-gate error). |
| D3 | Resolve with correct id, `--disposition resume` | Suspension cleared; instance **still active/test** (exact parked phase); revision bumped; `workflow.suspension_resolved` event with `disposition=resume`; task document STILL untouched. |
| D4 | After D3: claim + settle `test_result result=done` | Advances to active/test_review normally — the park round-trip left no residue; pre-park evidence still fresh. |
| D5 | Park again on the same instance | NEW suspension id ≠ the B1 id (occurrence identity). |
| D6 | Resolve using the OLD (B1) id | Refused — stale id must not resolve a later park. |
| D7 | Resolve with current id, `--disposition close` | Instance closed/done; suspension cleared; **task state = completed** (the close disposition effect executed — this also proves `semanticKey` placeholder expansion works in resolution context); event `disposition=close`. |
| D8 | Fresh instance: park, resolve `--disposition cancel` | Instance closed/cancelled; **task state = cancelled**; event `disposition=cancel`. |
| D9 | Resolve on an instance with no active suspension | Refused: no active suspension. |

## E. Watch

| # | Case | Assert |
|---|------|--------|
| E1 | `wrkf watch --until suspended` running during a park | Wakes on the park. |
| E2 | `wrkf watch --until waiting` during a park | Does NOT wake (parks are not status=waiting in v5); document as expected behavior. |

## F. Claim succession (engine contract, exercised via @5 actions)

| # | Case | Assert |
|---|------|--------|
| F1 | First claim with priorRun null | Accepted. |
| F2 | Second claim while the first lease is live | Refused (exclusivity). |
| F3 | Claim with a short `--lease-ms`, let it expire, NO successor; original claimer settles late | **Accepted.** Expiry alone forfeits nothing. |
| F4 | Expired lease; successor claims WITHOUT priorRun | Refused; refusal payload carries the FULL predecessor record — assert every field present: owner, claimedAt, heartbeatAt, leaseExpiresAt, settle status, sideEffectClasses, externalRunRef, workspaceRef, evidence refs. Payload completeness is the loop's API — missing field = FAIL, not cosmetic. |
| F5 | Successor re-claims WITH the predecessor's run id | Accepted; predecessor terminalized as **superseded** (not "failed"); succession event in the ledger. |
| F6 | After F5: the evicted predecessor attempts to settle | Refused with "superseded by <run>". |
| F7 | Over a cleanly SETTLED prior attempt (e.g. after a `fail` rewind), claim without naming it | Refused — uniformity: every retry names its predecessor. |

## G. Full lifecycle (single direct-landing tail)

| # | Case | Assert |
|---|------|--------|
| G1 | End-to-end: test → test_review(pass) → implement(done) → verify(pass) → gate(pass) → land(landed) | closed/done; task completed; no `workflow.lane` fact supplied; all sourceBinding/settleValidation contracts enforced en route (wrong `source_identity` on verify refused — spot-check one refusal). |
| G2 | Gate `pass` evidence containing only `result=pass` plus the existing range fact | Advances directly to active/land; no trunk or awaiting-merge arm exists. |
| G3 | From land, settle `landing_result result=operator_required` without a lane fact | Instance suspends in active/land. Resolve resume → back in active/land exactly. |
| G4 | verify `other` outcome (settle a verify_result that matches no explicit arm) | Suspends via the `otherwise` arm — @4's block-verify-other gate preserved in suspend form. |

## H. Purge confirmations

| # | Case | Assert |
|---|------|--------|
| H1 | CLI surface | No `wrkf suspend` command (entry is template-only); `wrkf suspension resolve` exists; no reap command; no workspace lease commands. |
| H2 | Events/DTOs | No contextHash stamped anywhere (instance, events, webhook payloads). |

---

Execution notes: run A fully first (cheap, offline). B–E on one scratch instance-family, F on its own instances (lease timing), G last (longest). Any FAIL stops the run and comes back to mable@agent-loop with the case number before anything lands.
