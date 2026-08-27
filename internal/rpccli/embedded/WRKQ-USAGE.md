<task_tracking_rules>
# wrkq Task Management CLI

## Task Lifecycle
1. **Before starting a task**: Set task to `in_progress`
   wrkq set T-00001 --state in_progress

2. **During work on a task**: Add progress comments for significant milestones
   wrkq comment add T-00001 -m "Implemented core logic in cmd/apply.go"
   wrkq comment add T-00001 -m "Added test coverage, 3 edge cases found"
   wrkq comment add T-00001 -m - <<'EOF'
   Multi-line progress note from stdin.
   EOF

3. **Before completing a task**: Add final summary comment
   wrkq comment add T-00001 -m "Completed. Added apply cmd with 3-way merge support. Updated docs. All tests passing."
   wrkq set T-00001 --state completed

## TodoWrite tasks
Update the wrkq task **before** using the TodoWrite tool. When using the TodoWrite tool, include the wrkq task id in parenthesis whenever possible.

## Naming Conventions
1. One-off tasks should be created/tracked in the **inbox** container.
2. Use short, descriptive slugs for tasks. (e.g. `login-auth-flow`, `logout-auth-flow`)

## Managing Containers
wrkq mkdir myfeat

## Use `--project` to change default project:
wrkq projects --json             # List all available projects
wrkq set wrkq --root ~/praesidium/wrkq  # Register a project checkout root
wrkq ls --project agent-spaces inbox          # List tasks in agent-spaces/inbox
wrkq find --project myproject --state open     # Find open tasks in myproject

## Finding Tasks
wrkq find inbox --state open
wrkq find --label refactor --label urgent --state all --type t
wrkq find --sort updated_at --reverse --limit 5
wrkq index update
wrkq search 'query text' --ndjson
wrkq search 'query text' --label refactor --state all
wrkq search 'query text' --sort updated_at --reverse --limit 5
wrkq tree myfeat --json

`--label VALUE` is a repeatable exact-membership read filter; every requested
label must be present. Matching is case-sensitive, and duplicate filters are
idempotent. The plural write flag on task and campaign mutations accepts either
comma-separated shorthand (`--labels "urgent, agent"`) or a JSON string array.
Only a trimmed value beginning `[` selects JSON; every other value, including
JSON-looking text such as `null`, is shorthand. Shorthand trims segments and
drops empty segments; use JSON when a label itself contains a comma or when
element whitespace/empty values must be preserved. `--labels ""` and
`--labels '[]'` both clear labels; omitting the flag leaves labels unchanged.

## Reading Tasks
wrkq cat T-00001 --json

## List tasks
wrkq ls inbox --type t --sort updated_at --reverse --limit 5

## Create task. Always use HEREDOC for description
wrkq touch inbox/task-slug --state open --priority 2 -t "New Task" -d - <<'EOF'
Multi-line markdown description goes here.

- bullet one
- bullet two
EOF

## Stdin conventions
- `-` reads stdin for text flags and body positions, e.g. `-d -`, `--specification -`, `comment add T-00001 -m -`, `handoff create --body-file -`.
- `@file` reads file content for text flags, e.g. `-m @notes.md`.
- Use only one stdin consumer per command invocation.
- Dash-stdin on a terminal errors; pipe input or use a heredoc.
- Destructive commands with piped selector input may also need `--yes` because confirmation prompts read stdin.

## Deleting Tasks
wrkq rm inbox/task-slug
wrkq rm inbox/task-slug --purge --yes

## Set task state/priority/fields (quick updates)
wrkq set T-00001 --state in_progress
wrkq set T-00001 --title "New title" --due-at 2025-12-01
wrkq set T-00001 --state in_progress --priority 1 --description "Starting work"
wrkq set T-00001 --outcome -       # Curated result from stdin; blank clears

Supported states: idea, draft, open, in_progress, completed, blocked, cancelled, archived, deleted
Priority: 1-4
Common fields: state, priority, title, labels, due_at, start_at, description, specification, caused_by
Outcome is the curated plain-terms result; final comments remain the worker's raw record. Completion never requires an outcome.
Lineage: --caused-by T-XXXXX[,T-YYYYY] records the task(s) whose delivered work caused this defect/rework (wrkq set ... --caused-by "" clears it; wrkq find --caused-by T-XXXXX lists attributed tasks).
Reserved label: needs_smoketest requests Smokey through webhook automation; it is not a state.
Run `wrkq set --help` for the full current field surface.

## Add comment
wrkq comment add T-00001 -m "Starting implementation at 10:00am"
wrkq comment add T-00001 -m - <<'EOF'
Starting implementation at 10:00am
EOF

## Task history
wrkq log T-00001 --oneline
wrkq log T-00001 --patch      # Show detailed changes

## Promises (future attention, not task deadlines)
```bash
wrkq promise add --subject "Revisit rollout" --in 7d
wrkq promise add --for lance --on-behalf --review-at 2026-09-01T15:00:00-05:00 --subject "Review rollout"
wrkq promise add --task T-00001 --in 36h --question "What remains?"
wrkq promise list                         # Current principal's promises
wrkq promise list --task T-00001          # All owners watching this task
wrkq promise ready --for lance
wrkq promise ready --for lance --project wrkq --include-global
wrkq promise renew PR-00001 --in 7d --note "Still active"
wrkq promise resolve PR-00001 --note "Satisfied"
wrkq promise abandon PR-00001 --note "Superseded"
wrkq promise attach PR-00001 --campaign rollout/wave
wrkq promise detach PR-00001
wrkq cat PR-00001 --json --one
wrkq log PR-00001 --patch
wrkq rm PR-00001                          # Deliberately abandons
wrkq rm PR-00001 --purge --yes            # Permanently deletes
```

Exactly one of `--review-at` or `--in` is required by `promise add` and
`promise renew`. The CLI forwards both values unchanged; the API normalizes
absolute timestamps and resolves relative durations against server time.
`--task` and `--container` are exclusive; `--campaign` aliases `--container`.
Promise mutations accept `--etag` or `--if-match`. `--subject`, `--question`,
and `--note` follow the standard literal/`@file`/`-` stdin conventions.
There are no `promise show` or `promise history` commands: use root `cat` and
`log`. `wrkq check` and `wrkq tree` surface ready/attached promises; tree shows
open promise leaves by default and includes closed leaves with `--state all`.

## Output Formats
- `--json` - Pretty JSON
- `--ndjson` - Newline-delimited JSON (best for parsing)
- `--output MODE` - table, human, json, ndjson, porcelain, yaml, tsv, raw
- `--porcelain` - Stable modifier; mirrors next_cursor on stderr where applicable

Defaults: non-TTY list/search/history commands use NDJSON; singleton, detail, mutation, and content commands use JSON. Use `--output raw` when a pipeline needs raw markdown from `cat`.

`wrkq cat ... --json` is always array-shaped, including one selector. For a bare
singleton object, use `wrkq cat ID --json --one`. `--one` requires exactly one
explicit selector and one resolved task or promise, refuses non-JSON output modes, and emits
no partial JSON on failure. Add `--porcelain` for compact singleton JSON.

## Daemon (`wrkq server`)
```bash
wrkq server status        # pid, endpoint, launchd job, installed-vs-running binary
wrkq server health        # fails if the daemon is down OR armed to die on respawn
                          # (a 401 is liveness-only, not a failure: the daemon answered)
wrkq server restart       # reload the launchd job, verified by a live answer
```

On macOS `restart` reloads the launchd job (`bootout`, wait for it to unload,
`bootstrap`) rather than `kickstart -k`, and reports success only once the
daemon answers. launchd pins a code requirement to the cdhash of the binary
present at bootstrap; `go build` re-signs adhoc on every build, so a respawn of
a rebuilt `wrkqd` inside the old job is SIGKILLed (`OS_REASON_CODESIGNING`) and
logs nothing. Only a bootout/bootstrap cycle re-derives the requirement.

An install without a restart therefore leaves a healthy-looking daemon armed to
die at its next respawn, however much later. `just install` warns when it
replaces a `wrkqd` a running job still holds, `wrkq server status` reports
`binaryStale`, and `wrkq server health` fails on it. On the canonical node,
install and restart together.

`health` resolves the daemon's token like the CLI transport does (`WRKQD_TOKEN`,
else `WRKQD_TOKEN_FILE`); on a per-node-token daemon an unauthenticated caller
gets `auth: unauthorized` alongside `status: ok`, since a 401 still proves the
daemon answered.

## Caller principal
- Canonical caller authority is `agent:<id>`.
- Use `--principal-ref agent:<id>` / `--as agent:<id>` or `WRKQ_PRINCIPAL_REF=agent:<id>`.
- For wrkf, use `--principal-ref agent:<id>` or `WRKF_PRINCIPAL_REF=agent:<id>`.
- Runtime task/project context belongs in scope/delivery refs, not the principal.
- `WRKQ_ACTOR`, `WRKF_ACTOR`, actor UUIDs / `A-*` ids, bare slugs, `system:*`, and config `default_actor` are not caller authority.

</task_tracking_rules>
