# 8. Workflows (wrkf)

wrkf attaches versioned workflow templates to wrkq tasks and moves them through declared states. A transition
moves only when its role is allowed, its required evidence exists, and its checks (hooks from the daemon's hook
catalog) return a verdict that selects an outcome. Ledger, obligations, effects, suspensions, runs and a
supervisor role sit on top. Code: `cmd/wrkf/`, `internal/wrkfcli/`, `internal/workflow/` (`transition.go`,
`service.go`, `suspension_resolve.go`, `instance_cancel.go`), `internal/wrkfapi/`, and `internal/workrpc/`
(`wrkf.*` methods). Contract: `docs/wrkf-rpc.md`, `docs/wrkq-wrkf-rpc.md`. Sample template: `pbc/`.

## Sub-features

- **Templates.** `wrkf workflow validate|install|list|show|diff <file|ref>`. The template schema is
  `wrkf.workflow-template.v0`.
- **Attach.** `wrkf task attach <task> --workflow id@version` creates an instance at the template's `initial`
  state, revision 0. `--supersede` replaces a live instance.
- **Next.** `wrkf next <task> --role R --json` lists the role's actions (for example `collect_evidence`, with
  `why` and `unblocks`) and `blockedTransitions` with what each one is waiting on.
- **Evidence.** `wrkf evidence add <task> --kind K --ref REF --facts '{...}' --summary ...` creates `ev_<n>`.
- **Transition.** `wrkf transition <task> <id> [--run-checks] [--dry-run] [--expect-revision N]`. A blocked
  transition fails with `WRKF_TRANSITION_BLOCKED`. With `--run-checks`, the hook runs and its exit selects the
  outcome (`checkVerdict`).
- **Inspect.** `wrkf task inspect|instances|timeline|refresh <task>`. The timeline holds `workflow.attached`
  and `workflow.transitioned` events.
- **The rest.** `wrkf ledger append|list`, `wrkf obligation`, `wrkf effect`, `wrkf suspension`, `wrkf instance cancel`,
  `wrkf run`, `wrkf supervisor`, `wrkf hook`, `wrkf check`, `wrkf watch`, `wrkf action`.

## How to get to it

Run `eval "$(wv env <name>)"`. The scratch daemon's hook catalog is `$WV_STATE/hooks.json`, with `wv_pass`
(`/usr/bin/true`) and `wv_fail` (`/usr/bin/false`). Hooks run inside the **daemon**, so a template can only
reference hook ids from that catalog. The fixture template is `wv-flow.json` in the evidence dir: plan → done
(it needs `implementation` evidence with `verdict=ready`) → closed (the `wv_pass` check).

## Driving it

```bash
WF=<artifact_dir>/08-workflows/wv-flow.json      # copy from var/wrkq-artifacts/T-10298/08-workflows/wv-flow.json
R="wrkf --principal-ref agent:clod --role coordinator"
wrkf workflow validate $WF --json && $R workflow install $WF --json
wrkq touch inbox/wv-flow -t "WV workflow task"                     # T-00005 on a fresh drive
$R task attach T-00005 --workflow wv_flow@1 --json
$R next T-00005 --json | jq -c '{actions: [.actions[].id], blocked: [.blockedTransitions[].id]}'
$R transition T-00005 plan_ready --json                           # WRKF_TRANSITION_BLOCKED
$R evidence add T-00005 --kind implementation --ref wv:drive --facts '{"verdict":"ready"}' --summary 'WV evidence' --json
$R transition T-00005 plan_ready --json
$R transition T-00005 finish --run-checks --json
$R task inspect T-00005 --json | jq -c '{status, outcome, revision, templateId}'
$R task timeline T-00005 --json | jq -c '[.events[].type]'
```

To drive the failing branch, install a copy of the template whose check uses `wv_fail`. Its `otherwise`
outcome lands in `phase: error`.

## Gotchas

- **The workflow's closed state doesn't change the task's state.** After the workflow closed `completed`,
  `wrkq cat` still said `state: open` (2026-10-05, T-10298 `08-workflows/drive.txt`). Task state and workflow
  state are separate. Close the task yourself.
- `wrkf transition --json` and `task attach --json` don't nest the result under `.instance`. Read the final
  state with `task inspect` (`status`, `phase`, `outcome`, `revision`).
- `wrkf` takes `--json`. `wrkq`'s campaign verbs take `--output json` instead (feature 5).
- A remote client can't pass `--hook-catalog`. The catalog belongs to the canonical node, and `@wrkq/client`
  throws `hookCatalogPath is local-only` in remote mode.
- Never install a test template on the canonical daemon. Templates are durable and shared, and a wrong install
  can only be discontinued, not removed.

## Proven when

On the scratch, the template validates and installs. The attached instance starts at `plan`, revision 0. `next`
names the missing evidence. The transition is refused until that evidence exists. `plan_ready` and then
`finish --run-checks` leave the instance `closed`/`completed` at revision 2, with a timeline of attached plus
two transitioned events.

Driven 2026-10-05 on wv `t-10298` (T-10298): `var/wrkq-artifacts/T-10298/08-workflows/drive.txt` with the fixture
`08-workflows/wv-flow.json`. Live read: `wrkf workflow list` (31 templates) in `live/canonical.txt`.
