# 4. Claims and promises

**Claims** give a task one holder: a principal, a task-scoped seat, an authenticated node and a generation. A
takeover supersedes the holder and bumps the generation, and only the current holder's token can release. A
**promise** is a future-attention record about a task or container, with a review time and a review question.
It is renewed, resolved or abandoned after review. Code: `internal/rpccli/claim.go`, `internal/rpccli/promise.go`,
`internal/nodeauth/`, and `internal/store/` (claims, promises). Background: AGENTS.md "Federated Task Claims".

## Sub-features

- **Claim.** `wrkq claim <task> --scope agent:<id>:project:<p>:task:<T> [--take-over --yes] --json` returns
  `{claimedBy, claimedScope, claimedNode, claimGeneration, claimToken}`. The daemon derives `claimedNode` from
  the bearer credential. Claiming moves an open task to `in_progress`. A completed task is refused
  `wrong_state: completed`. A `blocked` task is claimable and moves to `in_progress` (driven 2026-10-20, T-11374).
  Any other state is refused (source read, `claims.go`).
- **Contention.** A second claimant is refused with `already_claimed: holder=... node=... scope=... generation=N`.
  `--take-over` supersedes and bumps the generation.
- **Release.** `wrkq release <task> --scope ... --claim-token ... --claim-generation N`, run as the holder's
  principal. A superseded holder is refused (`claim_superseded: ...`), and so is a token that doesn't match the
  current claim, with the same message. Release prints the prior claim, records `task.claim_released`, and leaves
  the task state as it is. A holder re-claiming its own task is refused `already_claimed`. `--force` is the operator path.
- **Reading a claim.** `wrkq cat <task> --json --one` carries the holder as flat fields: `claimed_by`,
  `claimed_scope`, `claimed_node`, `claimed_at`, `claim_generation`. There is no nested `claim` object
  (2026-10-20, T-11374).
- **Promises.** `wrkq promise add --task|--container ... --subject ... --question ... --review-at|--in ...` creates
  `PR-<n>`. `promise ready` lists the promises that are due, `promise renew --if-match E --in 7d --note ...` sets
  the next review, and `promise resolve|abandon --if-match E --note ...` closes one. `promise list`,
  `promise attach|detach|edit` also exist. `wrkq log PR-<n>` gives the history. `--subject` defaults to the
  task's title. Only the owner sees a promise in `ready` or can change it (others get `WRKQ_FORBIDDEN: only the
  promise owner may mutate it`, driven 2026-10-20).

## How to get to it

Run `eval "$(wv env <name>)"`. The scratch daemon has two node credentials: node `wv` (`$WV_STATE/token`, the
`wv env` default) and node `wv2` (`$WV_STATE/token-wv2`). Act as the second node with
`WRKQD_TOKEN_FILE=$WV_STATE/token-wv2`.

## Driving it

```bash
# Claim T-00002: feature 2 leaves T-00001 completed, and a completed task is refused wrong_state.
S1=agent:clod:project:wv-<name>:task:T-00002; S2=agent:cody:project:wv-<name>:task:T-00002
wrkq claim T-00002 --scope $S1 --as agent:clod --json | tee "$WV_STATE/claim1.json"
wrkq claim T-00002 --scope $S2 --as agent:cody --json                       # already_claimed
WRKQD_TOKEN_FILE=$WV_STATE/token-wv2 wrkq claim T-00002 --scope $S2 --as agent:cody --take-over --yes --json | tee "$WV_STATE/claim2.json"
wrkq release T-00002 --scope $S1 --claim-token "$(jq -r .claimToken "$WV_STATE/claim1.json")" --claim-generation 1 --json   # claim_superseded
WRKQD_TOKEN_FILE=$WV_STATE/token-wv2 wrkq release T-00002 --as agent:cody --scope $S2 \
  --claim-token "$(jq -r .claimToken "$WV_STATE/claim2.json")" --claim-generation 2 --json
wrkq promise add --task T-00001 --subject "WV promise" --question "Still green?" --review-at 2026-01-01T00:00:00Z --json
wrkq promise ready --json | jq -c '[.[] | {id,state,ready,etag}]'
wrkq promise renew PR-00001 --if-match 9 --in 7d --note stale --json      # WRKQ_CONFLICT
wrkq promise renew PR-00001 --if-match 1 --in 7d --note "WV reviewed" --json
wrkq promise resolve PR-00001 --if-match 2 --note "WV done" --json
wrkq log PR-00001 --oneline
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
  Set `--as` to match the scope's agent.
- `promise add --json` returns camelCase fields (`reviewAt`, `ready`, `state`, `etag`), and `promise ready --json`
  returns a bare array.
- Closing a campaign records a `container.campaign_close_nudged` event (feature 5) alongside its state change.

## Proven when

One holder at a time: the second claimant gets `already_claimed`, the takeover from node `wv2` reports
generation 2 and `claimedNode: wv2`, the superseded holder can't release, and the new holder can. The event
feed (`wrkq monitor watch --raw`, feature 9) shows both `task.claimed` events, the second with
`take_over: true`, then `task.claim_released`. A due promise appears in `ready`, a stale etag is refused, renewing
it drops it from `ready`, and resolving it leaves `state: resolved` with three `promise.*` events in its log.

Driven 2026-10-26 on wv `t-11873` (T-11873 upkeep) with installed 7350485: `var/wrkq-artifacts/T-11873/04-claims-promises/drive.txt`.
T-11374's five zsh driver-error claims (see Gotchas) were not replayed.
