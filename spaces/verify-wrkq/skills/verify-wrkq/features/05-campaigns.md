# 5. Campaigns

A campaign adorns an ordinary container with a lifecycle (draft → active → completed|cancelled), a brief and a
specification. Tasks inside the container are **resident** members. A task elsewhere becomes an **enrolled**
member and keeps its own project and path. Code: `internal/rpccli/campaign.go`, `internal/rpccli/set.go`
(`--campaign`), `internal/store/containers.go`, and `internal/taskmember/`.

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
wrkq campaign close P-<camp1 id> --state completed --json | jq -c '{previousState,campaignState,missingOutcomes}'
wrkq log P-<camp1 id> --oneline
```

## Gotchas

- Campaign verbs take `--json` or `--output json` (both force JSON on a TTY; a non-TTY stdout gets JSON anyway).
- An unknown `wrkq campaign <verb>` prints the group help instead of an error. `enroll` is not a verb.
  Enrollment is `wrkq set --campaign`.
- `wrkq stat <container> --json` returns an array, and `wrkq cat P-<n> --json --one` doesn't carry the
  campaign state. Read the state from the `convert`/`activate`/`close` results or from `portfolio`.
- After `close`, the campaign drops out of the default `portfolio` aggregate.
- Named subtasks can't enroll in campaigns (`wrkq info`).

## Proven when

`convert` reports `campaignState: draft` and `activate` makes it `active`. The enrolled task keeps its inbox
path and shows `membership: enrolled`. `find --campaign` returns both the resident and the enrolled member.
`close` reports `previousState: active` → `completed` and names the members without outcomes. The container's
log has `container.campaign_state_changed` for each move.

Driven 2026-10-05 on wv `t-10298` (T-10298): `var/wrkq-artifacts/T-10298/05-campaigns/drive.txt`.
