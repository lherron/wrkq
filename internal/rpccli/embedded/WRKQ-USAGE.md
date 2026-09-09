<task_tracking_rules>
# wrkq agent guide

Use these recipes directly when the inputs are known. Consult `wrkq <command> --help` for an undocumented operation, missing option, or rejected syntax.

## Scope and output

Commands use the current project; `--project <project>` selects another. Use the project's scope for its work. `wrkq projects --json` lists projects.

Mutation identity normally comes from the runtime. When supplying it explicitly, use `--as agent:<id>` or `WRKQ_PRINCIPAL_REF=agent:<id>`. Project/session scope is separate from caller identity; legacy `WRKQ_ACTOR` is not authority.

Use `--json` for structured results, `--ndjson` for streams. `wrkq cat ID --json --one` returns one object; without `--one`, JSON `cat` returns an array. `--output raw` reads task Markdown. Follow returned pagination cursors when more results are needed.

## Find and read work

```bash
wrkq tree
wrkq ls inbox --type t --sort updated_at --reverse --limit 5
wrkq find --state open
wrkq find --label refactor --label urgent --state all --type t
wrkq search 'query text' --state all --limit 10
wrkq cat T-00001 --json --one
wrkq log T-00001 --oneline
wrkq log T-00001 --patch
```

Repeated `--label` filters require every label, matching exactly and case-sensitively. Use `--state all` when looking for completed or otherwise hidden work. Run `wrkq index update` before search when freshness matters.

## Create and update work

Create one-off tasks under `inbox` with short descriptive slugs. Use `wrkq mkdir <path>` when a new container is needed.

```bash
wrkq touch inbox/task-slug --state open --priority 2 -t 'Task title' -d - <<'BODY'
Describe the intended behavior, scope, and evidence that proves completion.
BODY
```

Use quoted heredocs for multiline text so backticks and shell variables stay literal. Text flags accept `-` for stdin or `@file` for file contents; use one stdin consumer per command.

```bash
wrkq set T-00001 --state in_progress
wrkq set T-00001 --title 'Revised title' --priority 1
wrkq set T-00001 --specification @spec.md
wrkq set T-00001 --labels 'refactor, urgent'
wrkq comment add T-00001 -m - <<'BODY'
Record a significant milestone, decision, or blocker with supporting evidence.
BODY
```

Set work `in_progress` before starting. Before setting `completed`, verify the result and add a final comment with changes, validation evidence, and remaining limitations. Record blockers explicitly.

```bash
wrkq comment add T-00001 -m 'Completed: <changes>; verified by <evidence>.'
wrkq set T-00001 --state completed
```

States: `idea`, `draft`, `open`, `in_progress`, `completed`, `blocked`, `cancelled`, `archived`, `deleted`. Priority: 1–4.

`--labels` replaces labels; `--labels ''` clears them. Use a JSON array for labels containing commas. `needs_smoketest` requests Smokey through automation; it is not a state.

`--outcome` records a curated result; it is optional for completion. `--caused-by T-00002` records the delivered task that caused defect/rework. `wrkq rm <task>` soft-deletes a task.

A task inside a campaign is already a member. Enroll a task elsewhere with `wrkq set T-00001 --campaign P-00001`; it keeps its project and path. Read all members with `wrkq find --campaign P-00001 --state all`.

## Wait for work

```bash
wrkq monitor wait T-00001 --until state=completed --timeout 30s
wrkq cat T-00001 --json --one
```

Choose the required state and a bounded timeout. A timeout is not completion; read the task and its evidence before proceeding.

## Transfer session context

```bash
wrkq handoff create --scope astra@agents -t 'Resume work' --body-file - <<'BODY'
Record the objective, decisions, durable evidence, remaining work, and next action.
BODY
wrkq handoff list --scope astra@agents --json
wrkq handoff get H-00001 --json
wrkq handoff acknowledge H-00001 --if-match 1 --note 'Consumed; <retained context>' --json
```

Replace the example scope with the intended agent/project. List defaults to pending and includes full bodies and etags; read those bodies and skip `get` when already complete. Use `get` for a known ID or incomplete output. Acknowledge with the returned etag, not the example value. Acknowledgement retires the handoff from pending listings; it does not complete the work described in it.

## Schedule future attention

Promises commit you to revisit a subject; they are separate from task deadlines.

```bash
wrkq promise add --task T-00001 --in 7d --subject 'Review progress'
wrkq promise list
wrkq promise ready
wrkq promise renew PR-00001 --in 7d --note 'Still active'
wrkq promise resolve PR-00001 --note 'Satisfied'
wrkq promise abandon PR-00001 --note 'Superseded'
```

For `add` and `renew`, use either `--in <duration>` or `--review-at <timestamp>`. Read details/history with `wrkq cat PR-00001 --json --one` and `wrkq log PR-00001 --patch`.

## Related tools

`wrkc info` covers durable rooms, addressed messages, and reply obligations. `wrkp info` covers project facts and timelines. Installation and daemon operation belong to the wrkq repository's `AGENTS.md` and operations documentation.
</task_tracking_rules>
