# 8. Workflows (wrkf)

wrkf attaches versioned workflow templates to wrkq tasks and moves them through declared states. A transition
moves only when its role is allowed, its required evidence exists, and its checks (hooks from the daemon's hook
catalog) return a verdict that selects an outcome. Ledger, obligations, effects, suspensions, runs and a
supervisor role sit on top. Code: `cmd/wrkf/`, `internal/wrkfcli/`, `internal/workflow/` (`transition.go`,
`service.go`, `suspension_resolve.go`, `instance_cancel.go`), `internal/wrkfapi/`, and `internal/workrpc/`
(`wrkf.*` methods). Contract: `docs/wrkf-rpc.md`, `docs/wrkq-wrkf-rpc.md`. Sample template: `pbc/`.

## Sub-features

- **Templates.** `wrkf workflow validate|install <file>`, `diff <old file> <new file>`, `list`, and
  `show|discontinue|reinstate <id@version>`. The template schema is `wrkf.workflow-template.v0`. Re-installing the
  same file returns `installed: false`. Source read, not driven: the daemon's hook catalog is pinned into the
  template at install, and a later catalog change makes check runs fail `hook catalog hash mismatch`.
- **Attach.** `wrkf task attach <task> --workflow id@version` creates an instance at the template's `initial`
  state, revision 0. `--supersede` replaces a live instance, and it needs `--predecessor-instance <wfi_...>` and
  `--predecessor-revision N`. Alone it is refused `WRKF_VALIDATION: predecessor instance id is required for supersede`.
- **Next.** `wrkf next <task> --role R --json` lists the role's actions (for example `collect_implementation` for the fixture, with
  `why` and `unblocks`) and `blockedTransitions` with what each one is waiting on.
- **Evidence.** `wrkf evidence add <task> --kind K --ref REF --facts '{...}' --summary ...` creates `ev_<n>`.
  Facts of a declared kind are validated (`facts.verdict must be one of "ready", "needs_patch"`). An undeclared
  kind is accepted as free evidence (driven: `--kind nope` made `ev_000001`). Source read: only
  `wrkf evidence schema` refuses it.
- **Transition.** `wrkf transition <task> <id> [--run-checks] [--dry-run] [--expect-revision N]`. Missing
  evidence, or any check whose latest verdict isn't `pass`, blocks with `WRKF_TRANSITION_BLOCKED`. A wrong role
  is `WRKF_ROLE_DENIED`, a revision mismatch is `WRKF_STALE_REVISION`, and `--dry-run` leaves the revision alone.
  With `--run-checks`, the hook runs, its exit maps to a verdict through the check's `exitMap`, and a `pass`
  verdict selects the outcome (`checkVerdict`). Check runs persist as `chk_<n>` (`wrkf check list <task>`).
- **Inspect.** `wrkf task inspect|instances|timeline|refresh <task>`. The timeline holds `workflow.attached`
  and `workflow.transitioned` events.
- **The rest.** `wrkf ledger append|list`, `wrkf obligation`, `wrkf effect`, `wrkf suspension`, `wrkf instance cancel`,
  `wrkf run`, `wrkf supervisor`, `wrkf hook`, `wrkf check`, `wrkf watch`, `wrkf action`.

## How to get to it

Run `eval "$(wv env <name>)"`. The scratch daemon's hook catalog is `$WV_STATE/hooks.json`, with `wv_pass`
(`/usr/bin/true`) and `wv_fail` (`/usr/bin/false`). Hooks run inside the **daemon**, so a template can only
reference hook ids from that catalog. The fixture template is `../fixtures/wv-flow.json`: plan → done
(it needs `implementation` evidence with `verdict=ready`) → closed (the `wv_pass` check).
`../fixtures/wv-flow-fail.json` is the same flow as `wv_flow_fail@1`, with its check on `wv_fail`.

## Driving it

```bash
WF=<skill dir>/fixtures/wv-flow.json
R="wrkf --principal-ref agent:clod --role coordinator"
wrkf workflow validate $WF --json && $R workflow install $WF --json
wrkq touch inbox/wv-flow -t "WV workflow task" --json | jq -r '.[0].id'   # T-00005 when only the baseline blocks ran; use what it prints
$R task attach T-00005 --workflow wv_flow@1 --json
$R next T-00005 --json | jq -c '{actions: [.actions[].id], blocked: [.blockedTransitions[].id]}'
$R transition T-00005 plan_ready --json                           # WRKF_TRANSITION_BLOCKED
$R evidence add T-00005 --kind implementation --ref wv:drive --facts '{"verdict":"ready"}' --summary 'WV evidence' --json
$R transition T-00005 plan_ready --json
$R transition T-00005 finish --run-checks --json
$R task inspect T-00005 --json | jq -c '{status, outcome, revision, templateId}'
$R task timeline T-00005 --json | jq -c '[.events[].type]'
```

To drive the failing branch, install `fixtures/wv-flow-fail.json`, attach `wv_flow_fail@1` to a fresh task, add
the evidence and run `plan_ready`, then `finish --run-checks`. It is **refused** `WRKF_TRANSITION_BLOCKED`, and
the instance stays `active`/`done` at revision 1 with a `chk_` row of verdict `error`. The `otherwise` outcome
never fires for a failed check, because a non-pass verdict blocks before outcomes are evaluated (2026-10-05,
T-10349 `08-workflows/drive.txt`).

## Gotchas

- **The workflow's closed state doesn't change the task's state.** After the workflow closed `completed`,
  `wrkq cat` still said `state: open` (2026-10-05, T-10298 `08-workflows/drive.txt`). Task state and workflow
  state are separate. Close the task yourself.
- `wrkf transition --json` and `task attach --json` don't nest the result under `.instance`. Read the final
  state with `task inspect` (`status`, `phase`, `outcome`, `revision`).
- `wrkf` errors with `--json` print `{"error":{code,message}}` with exit 1. Source read, not driven
  (`wv rec` merges the streams): they go to stderr (`internal/wrkfcli/root.go`), so a stdout-only capture is empty.
- After the instance closes, the task's `meta.workflow.state` reads `closed`/`completed` while its `state` stays
  `open`.
- **A transition on a closed instance returns `WRKF_INTERNAL "internal error"`.** The from-state refusal is
  uncoded, so the cause is lost. Product task T-10356.
- **`wrkf ledger append` over `rpc://` is refused even with `--principal-ref`** (`requires an explicit canonical
  agent:<id> principal`). The same call in local mode succeeds. The wrkf remote transport doesn't send the
  principal. Product task T-10357.
- A remote client can't pass `--hook-catalog`. The catalog belongs to the canonical node, and `@wrkq/client`
  throws `hookCatalogPath is local-only` in remote mode. The wrkf CLI refuses with `--hook-catalog is local-only;
  hook catalog is canonical-node configuration in remote mode`.
- Never install a test template on the canonical daemon. Templates are durable and shared, and a wrong install
  can only be discontinued, not removed.

## Proven when

On the scratch, the template validates and installs. The attached instance starts at `plan`, revision 0. `next`
names the missing evidence. The transition is refused until that evidence exists. `plan_ready` and then
`finish --run-checks` leave the instance `closed`/`completed` at revision 2, with a timeline of attached plus
two transitioned events.

Driven 2026-10-05 on wv `t-10349` (T-10349 upkeep) with installed 037fe66: `var/wrkq-artifacts/T-10349/08-workflows/drive.txt`,
both fixtures, on T-00007 (pass) and T-00008 (fail). Live read: `wrkf workflow list --json` (31 templates, 16 active) in `live/reads.txt`.
