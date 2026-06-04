<task_tracking_rules>
# wrkq Task Management CLI

## Task Lifecycle
1. **Before starting a task**: Set task to `in_progress`
   wrkq set T-00001 --state in_progress

2. **During work on a task**: Add progress comments for significant milestones
   wrkq comment add T-00001 -m "Implemented core logic in cmd/apply.go"
   wrkq comment add T-00001 -m "Added test coverage, 3 edge cases found"

3. **Before completing a task**: Add final summary comment
   wrkq comment add T-00001 -m "Completed. Added apply cmd with 3-way merge support. Updated docs. All tests passing."
   wrkq set T-00001 --state completed

## TodoWrite tasks
Update the wrkq task **before** using the TodoWrite tool. When using the TodoWrite tool, include the wrkq task id in parenthesis whenever possible.

## Naming Conventions
1. One-off tasks should be created/tracked in the **inbox** container.
3. Use short, descriptive slugs for tasks. (e.g. `login-auth-flow`, `logout-auth-flow`)

## Managing Containers
wrkq mkdir myfeat

## Use `--project` to change default project:
wrkq projects --json             # List all available projects
wrkq ls --project agent-spaces inbox          # List tasks in agent-spaces/inbox
wrkq find --project myproject --state open     # Find open tasks in myproject

## Finding Tasks
wrkq find 'myfeat/api-feature/**' --state open
wrkq find --sort updated_at --reverse --limit 5
wrkq search 'query text' --ndjson
wrkq search 'query text' --sort updated_at --reverse --limit 5
wrkq tree myfeat --json

## Reading Tasks
wrkq cat T-00001 --json

## List tasks
wrkq ls myfeat/api-feature --type t --sort updated_at --reverse --limit 5

## Create task. Always use HEREDOC for description
wrkq touch myfeat/feature/task-slug --state open --priority 2 -t "New Task" -d - <<'EOF'
Multi-line markdown description goes here.

- bullet one
- bullet two
EOF


## Deleting Tasks
wrkq rm myfeat/api-feature/feature-slug
wrkq rm myfeat/api-feature/feature-slug --purge --yes

## Set task state/priority/fields (quick updates)
wrkq set T-00001 --state in_progress
wrkq set T-00001 --title "New title" --due-at 2025-12-01
wrkq set T-00001 --state in_progress --priority 1 --description "Starting work"

Supported states: open, in_progress, completed, blocked, cancelled
Priority: 1-4
Supported fields: state, priority, title, slug, labels, due_at, start_at, description, specification

## Add comment
wrkq comment add T-00001 -m "Starting implementation at 10:00am"

## Task history
wrkq log T-00001 --oneline
wrkq log T-00001 --patch      # Show detailed changes

## Output Formats
- `--json` - Pretty JSON
- `--ndjson` - Newline-delimited JSON (best for parsing)
- `--output MODE` - table, human, json, ndjson, porcelain, yaml, tsv, raw
- `--porcelain` - Stable modifier; mirrors next_cursor on stderr where applicable

Defaults: non-TTY list/search/history commands use NDJSON, singleton and mutation commands use JSON, and `cat` stays raw markdown unless explicitly overridden.

</task_tracking_rules>
