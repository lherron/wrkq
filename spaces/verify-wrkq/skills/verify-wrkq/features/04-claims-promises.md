# 4. Claims and promises

**Claims** give a task one holder: a principal, a task-scoped seat, an authenticated node and a generation. A
takeover supersedes the holder and bumps the generation, and only the current holder's token can release. A
**promise** is a future-attention record about a task or container, with a review time and a review question.
It is renewed, resolved or abandoned after review. Code: `internal/rpccli/claim.go`, `internal/rpccli/promise.go`,
`internal/nodeauth/`, and `internal/store/` (claims, promises). Background: AGENTS.md "Federated Task Claims".

## Sub-features

- **Claim.** `wrkq claim <task> --scope agent:<id>:project:<p>:task:<T> [--take-over --yes] --json` returns
  `{claimedBy, claimedScope, claimedNode, claimGeneration, claimToken}`. The daemon derives `claimedNode` from
  the bearer credential. Claiming moves an open task to `in_progress`.
- **Contention.** A second claimant is refused with `already_claimed: holder=... node=... scope=... generation=N`.
  `--take-over` supersedes and bumps the generation.
- **Release.** `wrkq release <task> --scope ... --claim-token ... --claim-generation N`, run as the holder's
  principal. A superseded holder is refused (`claim_superseded: ...`). Release leaves the task state as it is.
  `--force` is the operator path.
- **Promises.** `wrkq promise add --task|--container ... --subject ... --question ... --review-at|--in ...` creates
  `PR-<n>`. `promise ready` lists the promises that are due, `promise renew --if-match E --in 7d --note ...` sets
  the next review, and `promise resolve|abandon --if-match E --note ...` closes one. `promise list`,
  `promise attach|detach|edit` also exist. `wrkq log PR-<n>` gives the history.

## How to get to it

Run `eval "$(wv env <name>)"`. The scratch daemon has two node credentials: node `wv` (`$WV_STATE/token`, the
`wv env` default) and node `wv2` (`$WV_STATE/token-wv2`). Act as the second node with
`WRKQD_TOKEN_FILE=$WV_STATE/token-wv2`.

## Driving it

```bash
S1=agent:clod:project:wv-<name>:task:T-00001; S2=agent:cody:project:wv-<name>:task:T-00001
wrkq claim T-00001 --scope $S1 --as agent:clod --json | tee "$WV_STATE/claim1.json"
wrkq claim T-00001 --scope $S2 --as agent:cody --json                       # already_claimed
WRKQD_TOKEN_FILE=$WV_STATE/token-wv2 wrkq claim T-00001 --scope $S2 --as agent:cody --take-over --yes --json | tee "$WV_STATE/claim2.json"
wrkq release T-00001 --scope $S1 --claim-token "$(jq -r .claimToken "$WV_STATE/claim1.json")" --claim-generation 1 --json   # claim_superseded
WRKQD_TOKEN_FILE=$WV_STATE/token-wv2 wrkq release T-00001 --as agent:cody --scope $S2 \
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
- **`--scope` refuses a lane-suffixed session ref.** `agent:...:task:T-00001/lane:main`, which is the form
  `$HRC_SESSION_REF` takes, fails with `--scope must be a task-scoped sessionRef`. Drop `/lane:main`. When
  `--scope` is omitted, the runtime scope (with its lane) resolves correctly. This is a minor product defect.
- Release must run as the holder's principal: `WRKQ_VALIDATION: claim principal must match claim scope agent`.
  Set `--as` to match the scope's agent.
- `promise add --json` returns camelCase fields (`reviewAt`, `ready`, `state`, `etag`), and `promise ready --json`
  returns a bare array.
- Closing a campaign records a `container.campaign_close_nudged` event (feature 5) alongside its state change.

## Proven when

One holder at a time: the second claimant gets `already_claimed`, the takeover from node `wv2` reports
generation 2 and `claimedNode: wv2`, the superseded holder can't release, and the new holder can. The event
feed (`wrkq monitor watch --raw`, feature 9) shows both `task.claimed` events, the second with
`take_over: true`. A due promise appears in `ready`, a stale etag is refused, renewing
it drops it from `ready`, and resolving it leaves `state: resolved` with three `promise.*` events in its log.

Driven 2026-10-05 on wv `t-10298` (T-10298) with node tokens: `var/wrkq-artifacts/T-10298/04-claims-promises/drive.txt`.
The attempts above the NOTE line in that file ran before `wv` had node tokens.
