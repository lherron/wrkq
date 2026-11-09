# 4. Claims and promises

**Claims** give a task one holder: a principal, a task-scoped seat, an authenticated node and a generation. A
takeover supersedes the holder and bumps the generation, and only the current holder's token can release. A
**promise** is a future-attention record about a task or container, with a review time and a review question.
It is renewed, resolved or abandoned after review. Code: `internal/rpccli/claim.go`, `internal/rpccli/promise.go`,
`internal/nodeauth/`, `internal/wrkqapi/` (`claims.go`, `task_update.go`, promises), and `internal/store/` (claims,
promises, `envelope_lifecycle.go` for retry promises). Background: AGENTS.md "Federated Task Claims".

## Sub-features

- **Claim.** `wrkq claim <task> --scope agent:<id>:project:<p>:task:<T> [--take-over --yes] --json` returns
  `{claimedBy, claimedScope, claimedNode, claimGeneration, claimToken}`. The daemon derives `claimedNode` from
  the bearer credential. Claiming moves an open task to `in_progress`. A completed task is refused
  `wrong_state: completed`. A `blocked` task is claimable and moves to `in_progress` (driven 2026-10-20, T-11374).
  Any other state is refused (source read, `claims.go`). A workstream task is refused
  `WRKQ_WORKSTREAM_NOT_CLAIMABLE` (000072; driven 2026-11-09, T-12762).
- **Contention.** A second claimant is refused with `already_claimed: holder=... node=... scope=... generation=N`.
  `--take-over` supersedes and bumps the generation.
- **Release.** `wrkq release <task> --scope ... --claim-token ... --claim-generation N`, run as the holder's
  principal. A superseded holder is refused (`claim_superseded: ...`), and so is a token that doesn't match the
  current claim, with the same message. Release prints the prior claim, records `task.claim_released`, and leaves
  the task state as it is. A holder re-claiming its own task is refused `already_claimed`. `--force` is the operator path.
  Releasing a task nobody holds is refused `task is not claimed` (CLI `--json` code `WRKQ_ERROR`, T-12766).
- **Updating a claimed task.** A `set` on a claimed task needs the holder's credentials, or it is refused
  `claim_superseded: holder=...`: `WRKQ_CLAIM_TOKEN=<token> WRKQ_CLAIM_GENERATION=<n> AGENT_SCOPE_REF=<claim scope>
  wrkq set <task> --state completed --as agent:<holder>` succeeds. A raw `wrkq.task.update` with an `idempotencyKey`
  replays its first result even after a takeover moved the generation (a30b776), and a changed request under the
  key is `WRKQ_CONFLICT`. RPC-only `wrkq.task.claimValidate {task, principalRef, scope, claimGeneration,
  claimToken}` answers a stale token `WRKQ_CLAIM_SUPERSEDED`. Driven 2026-11-09, T-12762.
- **Reading a claim.** `wrkq cat <task> --json --one` carries the holder as flat fields: `claimed_by`,
  `claimed_scope`, `claimed_node`, `claimed_at`, `claim_generation`. There is no nested `claim` object
  (2026-10-20, T-11374).
- **Promises.** `wrkq promise add --task|--container ... --subject ... --question ... --review-at|--in ...` creates
  `PR-<n>`. `promise ready` lists the promises that are due, `promise renew --if-match E --in 7d --note ...` sets
  the next review, and `promise resolve|abandon --if-match E --note ...` closes one. `promise list`,
  `promise attach|detach|edit` also exist. `wrkq log PR-<n>` gives the history. `--subject` defaults to the
  task's title. Only the owner sees a promise in `ready` or can change it (others get `WRKQ_FORBIDDEN: only the
  promise owner may mutate it`, driven 2026-10-20). `promise add --for agent:<other>` needs `--on-behalf`
  (`WRKQ_FORBIDDEN: creating a promise for another owner requires onBehalf`); with it the owner is the other agent
  (driven 2026-11-09, T-12762). A `wrkc defer --retry-after` makes a retry promise that closes with
  `promise.resolved` (feature 6).
- **Bootstrap page.** The RPC-only `wrkq.promise.listView {states?, limit?, cursor?}` (5fbf10c, no CLI verb) pages
  every owner's promises in `(created_at, uuid)` order, `open` only by default. Feature 10 has its shape and
  refusals. Driven 2026-11-02, T-12309: `limit 1` returned clod's PR-00002 with a cursor, the next page cody's PR-00003.

## How to get to it

Run `eval "$(wv env <name>)"`. The scratch daemon has two node credentials: node `wv` (`$WV_STATE/token`, the
`wv env` default) and node `wv2` (`$WV_STATE/token-wv2`). Act as the second node with
`WRKQD_TOKEN_FILE=$WV_STATE/token-wv2`.

## Driving it

```bash
# Claim T-00002: feature 2 leaves T-00001 completed, and a completed task is refused wrong_state.
S1=agent:clod:project:wv-<name>:task:T-00002; S2=agent:cody:project:wv-<name>:task:T-00002
~/.local/bin/wrkq claim T-00002 --scope $S1 --as agent:clod --json | tee "$WV_STATE/claim1.json"
~/.local/bin/wrkq claim T-00002 --scope $S2 --as agent:cody --json                       # already_claimed
WRKQD_TOKEN_FILE=$WV_STATE/token-wv2 ~/.local/bin/wrkq claim T-00002 --scope $S2 --as agent:cody --take-over --yes --json | tee "$WV_STATE/claim2.json"
~/.local/bin/wrkq release T-00002 --scope $S1 --as agent:clod --claim-token "$(jq -r .claimToken "$WV_STATE/claim1.json")" --claim-generation 1 --json   # claim_superseded
WRKQD_TOKEN_FILE=$WV_STATE/token-wv2 ~/.local/bin/wrkq release T-00002 --as agent:cody --scope $S2 \
  --claim-token "$(jq -r .claimToken "$WV_STATE/claim2.json")" --claim-generation 2 --json
~/.local/bin/wrkq promise add --task T-00001 --subject "WV promise" --question "Still green?" --review-at 2026-01-01T00:00:00Z --as agent:clod --json
~/.local/bin/wrkq promise ready --as agent:clod --json | jq -c '[.[] | {id,state,ready,etag}]'
~/.local/bin/wrkq promise renew PR-00001 --if-match 9 --in 7d --note stale --as agent:clod --json      # WRKQ_CONFLICT
~/.local/bin/wrkq promise renew PR-00001 --if-match 1 --in 7d --note "WV reviewed" --as agent:clod --json
~/.local/bin/wrkq promise resolve PR-00001 --if-match 2 --note "WV done" --as agent:clod --json
~/.local/bin/wrkq log PR-00001 --oneline
```

## Gotchas

- **Claims need a node identity.** A daemon started with plain `-token` (or none) refuses every claim:
  `WRKQ_NODE_IDENTITY_REQUIRED: node identity is required for task claims`. `wv up` starts with
  `-node-tokens-file` for this reason (2026-10-05, T-10298).
- **zsh eats `$VAR:t...`.** Building a scope as `agent:clod:project:$WP:task:T-2` in zsh applies the `:t` (tail)
  modifier, so the scope becomes `...:wv-<name>ask:T-2` and every claim is refused
  `--scope must be a task-scoped sessionRef`. Brace it: `${WP}:task` (2026-10-20, T-11374 `04-claims-promises/drive.txt`).
- `--scope` accepts the lane-suffixed `$HRC_SESSION_REF` form (`...:task:T-00001/lane:main`) and records
  the claim against the task scopeRef with the lane dropped. A non-task ref is still refused.
- Release must run as the holder's principal: `WRKQ_VALIDATION: claim principal must match claim scope agent`.
  Set `--as` to match the scope's agent. The drive pins `--as agent:clod` on the superseded release and the promise
  lines, because without it the principal (and the promise owner) comes from whatever seat runs the drive
  (2026-11-09, T-12762 source read; the T-12309 replay passed only because the seat was clod's).
- `promise add --json` returns camelCase fields (`reviewAt`, `ready`, `state`, `etag`), and `promise ready --json`
  returns a bare array.
- `container.campaign_close_nudged` (feature 5) is recorded when the last direct member of an active campaign goes
  terminal, not by `campaign close` (2026-11-09, T-12762 `05-campaigns/drive.txt`: completing `m1` while the
  workstream member was open nudged nothing; completing the workstream nudged).

## Proven when

One holder at a time: the second claimant gets `already_claimed`, the takeover from node `wv2` reports
generation 2 and `claimedNode: wv2`, the superseded holder can't release, and the new holder can. The event
feed (`wrkq monitor watch --raw`, feature 9) shows both `task.claimed` events, the second with
`take_over: true`, then `task.claim_released`. A due promise appears in `ready`, a stale etag is refused, renewing
it drops it from `ready`, and resolving it leaves `state: resolved` with three `promise.*` events in its log. A
workstream is unclaimable, a claimed task completes only with the holder's credentials, and an on-behalf promise is
owned by the other agent.

Driven 2026-11-09 on wv `t-12762` (T-12762 upkeep) with installed 401c525: `var/wrkq-artifacts/T-12762/04-claims-promises/drive.txt`
(the T-12309 replay, then release edge cases, claimed completion, update replay, on-behalf and `claimValidate`
after the NOTE drift line). T-11374's five zsh driver-error claims (see Gotchas) are not replayed.
