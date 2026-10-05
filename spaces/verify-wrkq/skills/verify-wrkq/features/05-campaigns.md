# 5. Campaigns

A campaign adorns an ordinary container with a lifecycle (draft → active → completed|cancelled), a brief and a
specification. Tasks inside the container are **resident** members. A task elsewhere becomes an **enrolled**
member and keeps its own project and path. Code: `internal/rpccli/campaign.go`, `internal/rpccli/set.go`
(`--campaign`), `internal/store/campaign_lifecycle.go`, `internal/store/campaign_membership.go`,
`internal/wrkqapi/campaigns.go`, `internal/wrkqapi/campaign_portfolio.go`, and `internal/taskmember/`.

## Sub-features

- **Convert.** `wrkq campaign convert <container> --state draft|active -d <brief> --specification <spec>`.
- **Activate and close.** `wrkq campaign activate <container>` and
  `wrkq campaign close <container> --state completed|cancelled`. The close result lists `missingOutcomes`: the
  members without an outcome.
- **Edit.** `wrkq campaign edit` changes the brief and specification. Every edit is kept as a full-body
  `container.updated` event.
- **Membership.** Tasks in the container are resident. `wrkq set <task> --campaign P-<n>` enrolls a task from
  elsewhere, and `wrkq cat` then shows `campaign: {id, path, membership: resident|enrolled}`.
  `wrkq find --campaign P-<n> --state all` lists all members.
- **Portfolio.** `wrkq campaign portfolio --json` is the aggregate of every campaign.
- **Rooms.** `wrkc say T-<member>` coalesces to the campaign room (feature 6).

## How to get to it

Run `eval "$(wv env <name>)"`, then create a container in the scratch project to convert.

## Driving it

```bash
wrkq mkdir /wv-<name>/camp1 && wrkq touch camp1/member-a -t "resident member"
wrkq campaign convert camp1 --state draft -d "WV campaign brief" --specification "WV spec v1" --json
wrkq campaign activate camp1 --json
wrkq touch inbox/wv-enrolled -t "enrolled from inbox"
wrkq set inbox/wv-enrolled --campaign P-<camp1 id> && wrkq cat inbox/wv-enrolled --json --one | jq -c .campaign
wrkq find --campaign P-<camp1 id> --state all --json | jq -c '[.[] | {id,path}]'
wrkq campaign portfolio --json | jq -c '[.. | objects | select(.id? == "P-<camp1 id>")][0] | {id,campaignState}'
wrkq campaign close P-<camp1 id> --state completed --json        # refused: blocked by 2 open member(s)
wrkq set camp1/member-a inbox/wv-enrolled --state completed
wrkq campaign close P-<camp1 id> --state completed --json | jq -c '{previousState,campaignState,missingOutcomes}'
wrkq log P-<camp1 id> --oneline
```

## Gotchas

- Campaign verbs take `--json` or `--output json` (both force JSON on a TTY; a non-TTY stdout gets JSON anyway).
- An unknown `wrkq campaign <verb>` is a usage error, `unknown command "enroll" for "wrkq campaign"`, exit 2.
  `enroll` is not a verb. Enrollment is `wrkq set --campaign`.
- **A completed close needs every member terminal.** With open members, `close --state completed` is refused
  `campaign close blocked by N open member(s)`, with a hint naming each one and whether it is resident or
  enrolled. Complete, cancel, move or unenroll them first. `--state cancelled` abandons the campaign and leaves
  open members as they are (2026-10-05, T-10349 `05-campaigns/drive.txt`).
- `convert` with no `--state` makes an active campaign. A draft can only be activated or cancelled. A terminal
  campaign refuses every transition (`terminal campaigns cannot transition`), and enrolling into one is refused
  (`campaign enrollment target must be a draft or active campaign`).
- `wrkq stat <container> --json` returns an array, and `wrkq cat P-<n> --json --one` doesn't carry the
  campaign state. Read the state from the `convert`/`activate`/`close` results or from `portfolio`.
- After `close`, the campaign drops out of the default `portfolio` aggregate.
- Named subtasks can't enroll in campaigns: `subtask cannot have parent or campaign`.

## Proven when

`convert` reports `campaignState: draft` and `activate` makes it `active`. The enrolled task keeps its inbox
path and shows `membership: enrolled`. `find --campaign` returns both the resident and the enrolled member.
`close` with open members is refused, naming them. Once they are completed, `close` reports
`previousState: active` → `completed` and names the members without outcomes. The container's log
(`wrkq log camp1` works by path) has `container.campaign_state_changed` for each move and a
`container.campaign_close_nudged`.

Driven 2026-10-05 on wv `t-10349` (T-10349 upkeep) with installed 037fe66: `var/wrkq-artifacts/T-10349/05-campaigns/drive.txt`.
