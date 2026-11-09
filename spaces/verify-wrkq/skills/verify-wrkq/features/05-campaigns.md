# 5. Campaigns

A campaign adorns an ordinary container with a lifecycle (draft → active → completed|cancelled), a brief and a
specification. Tasks inside the container are **resident** members. A task elsewhere becomes an **enrolled**
member and keeps its own project and path. Code: `internal/rpccli/campaign.go`, `internal/rpccli/set.go`
(`--campaign`), `internal/store/campaign_lifecycle.go`, `internal/store/campaign_membership.go`,
`internal/wrkqapi/campaigns.go`, `internal/wrkqapi/campaign_portfolio.go`, `internal/taskmember/`, and for
create-as-campaign `internal/rpccli/containers.go` and `internal/store/containers.go` (T-12731).

## Sub-features

- **Convert.** `wrkq campaign convert <container> --state draft|active -d <brief> --specification <spec>`.
- **Create as a campaign.** `wrkq mkdir <path> --campaign[=draft|active]` (988b172, T-12731) creates the container
  and its campaign in one write: the log has `container.created` and `container.campaign_state_changed`, no
  `container.updated`. Bare `--campaign` is active. `--campaign=completed` is a usage error (exit 2) before any
  write. `mkdir -p <existing plain> --campaign` converts it (`converted:true`); on an existing campaign it changes
  nothing, even when the requested state differs (`-p --campaign=active` on a draft leaves it draft). Driven
  2026-11-09, T-12762.
- **Activate and close.** `wrkq campaign activate <container>` and
  `wrkq campaign close <container> --state completed|cancelled`. The close result lists `missingOutcomes`: the
  members without an outcome.
- **Edit.** `wrkq campaign edit` changes the brief and specification. Every edit is kept as a full-body
  `container.updated` event.
- **Membership.** Tasks in the container are resident. `wrkq set <task> --campaign P-<n>` enrolls a task from
  elsewhere, and `wrkq cat` then shows `campaign: {id, path, membership: resident|enrolled}`.
  `wrkq find --campaign P-<n> --state all` lists all members. `touch --campaign P-<n>` enrolls at create. Only
  **direct** children are resident: a task in `camp/sub/` isn't listed by `find --campaign`, doesn't block a close
  and doesn't hold off the close nudge. A resident workstream is an ordinary member and blocks a completed close.
  Driven 2026-11-09, T-12762.
- **Portfolio.** `wrkq campaign portfolio --json` is the aggregate of every campaign. A closed campaign drops out of
  the default aggregate; `portfolio --state completed` brings it back.
- **Rooms.** `wrkc say T-<member>` coalesces to the campaign room (feature 6).

## How to get to it

Run `eval "$(wv env <name>)"`, then create a container in the scratch project to convert.

## Driving it

```bash
~/.local/bin/wrkq mkdir /wv-<name>/camp1 && ~/.local/bin/wrkq touch camp1/member-a -t "resident member"
~/.local/bin/wrkq campaign convert camp1 --state draft -d "WV campaign brief" --specification "WV spec v1" --json
~/.local/bin/wrkq campaign activate camp1 --json
~/.local/bin/wrkq touch inbox/wv-enrolled -t "enrolled from inbox"
~/.local/bin/wrkq set inbox/wv-enrolled --campaign P-<camp1 id> && ~/.local/bin/wrkq cat inbox/wv-enrolled --json --one | jq -c .campaign
~/.local/bin/wrkq find --campaign P-<camp1 id> --state all --json | jq -c '[.[] | {id,path}]'
~/.local/bin/wrkq campaign portfolio --json | jq -c '[.. | objects | select(.id? == "P-<camp1 id>")][0] | {id,campaignState}'
~/.local/bin/wrkq campaign close P-<camp1 id> --state completed --json        # refused: blocked by 2 open member(s)
~/.local/bin/wrkq set camp1/member-a inbox/wv-enrolled --state completed
~/.local/bin/wrkq campaign close P-<camp1 id> --state completed --json | jq -c '{previousState,campaignState,missingOutcomes}'
~/.local/bin/wrkq log P-<camp1 id> --oneline
```

## Gotchas

- Hard deletion refuses surviving external enrollments with `WRKQ_VALIDATION`, naming task IDs and paths
  in the message and `data.members`. Both empty deletion and recursive purge refuse without mutation.
  Unenroll first; a recursive purge selecting both the campaign and its enrolled tasks succeeds
  (2026-10-26, T-11953 `05-campaigns/drive.txt`).

- Campaign verbs take `--json` or `--output json` (both force JSON on a TTY; a non-TTY stdout gets JSON anyway).
- An unknown `wrkq campaign <verb>` is a usage error, `unknown command "enroll" for "wrkq campaign"`, exit 2.
  `enroll` is not a verb. Enrollment is `wrkq set --campaign`.
- **A completed close needs every member terminal.** With open members, `close --state completed` is refused
  `campaign close blocked by N open member(s)`, with a hint naming each one and whether it is resident or
  enrolled. Complete, cancel, move or unenroll them first. `--state cancelled` abandons the campaign and leaves
  open members as they are (2026-10-05, T-10349 `05-campaigns/drive.txt`). Under `--json` the refusal is
  `code: WRKQ_WRONG_STATE` with the members in `hint` and the server's `data.stragglers` (and
  `missingOutcomes`) in `error.data` (T-12766, 2026-11-09).
- `convert` with no `--state` makes an active campaign. A draft can only be activated or cancelled. A terminal
  campaign refuses every transition (`terminal campaigns cannot transition`), and enrolling into one is refused
  (`campaign enrollment target must be a draft or active campaign: P-… is completed and accepts no new
  members`). Enrolling into a plain container gets the same prefix, then `P-… is a plain container; convert it first
  with …`.
- `mkdir --json` names no container id or state (`[{path, created[, converted]}]`). Read the id with
  `wrkq stat <path> --json` and the state from `portfolio`. A one-character slug under `--campaign` is
  `WRKQ_VALIDATION` like any container (2026-11-09, T-12762).
- `wrkq stat <container> --json` returns an array, and `wrkq cat P-<n> --json --one` doesn't carry the
  campaign state. Read the state from the `convert`/`activate`/`close` results or from `portfolio`.
- After `close`, the campaign drops out of the default `portfolio` aggregate.
- Named subtasks can't enroll in campaigns. `touch --subtask T-<n>.s --campaign P-<m>` is refused client-side
  `--subtask cannot combine with --parent-task or --campaign`; `set T-<n>.s --campaign P-<m>` is refused `failed to
  update task: subtask cannot have parent or campaign` (2026-11-09, T-12762).

## Proven when

`convert` reports `campaignState: draft` and `activate` makes it `active`. The enrolled task keeps its inbox
path and shows `membership: enrolled`. `find --campaign` returns both the resident and the enrolled member.
`close` with open members is refused, naming them. Once they are completed, `close` reports
`previousState: active` → `completed` and names the members without outcomes. The container's log
(`wrkq log camp1` works by path) has `container.campaign_state_changed` for each move and a
`container.campaign_close_nudged`, recorded when the last direct member goes terminal. `mkdir --campaign` makes an
active (or `=draft` a draft) campaign in one write, and a member under a subdirectory is not a member.

Driven 2026-11-09 on wv `t-12762` (T-12762 upkeep) with installed 401c525: `var/wrkq-artifacts/T-12762/05-campaigns/drive.txt`
(the T-12309 replay, then create-as-campaign, close refusal shape, residency, workstream member, enroll at create,
subtask refusals, portfolio filter and the enrolled-member delete refusal after the NOTE drift line).
