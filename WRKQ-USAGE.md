<task_tracking_rules>
# wrkq Task Management CLI

## Project-scoped usage
There is a WRKQ_PROJECT_ROOT in .env.local that scopes wrkq requests to only the current project.  If you need to see OTHER projects, run wrkq with WRKQ_PROJECT_ROOT= to change wrkq scope to true root.

## Task Lifecycle
1. **Before starting implementing a task**: Set task to `in_progress`
   ```bash
   wrkq set T-00001 --state in_progress
   ```

2. **Before completing a task**: Add final summary comment
   ```bash
   wrkq comment add T-00001 -m "Completed. Added apply cmd with 3-way merge support. Updated docs. All tests passing."
   wrkq set T-00001 --state completed
   ```

### Naming Conventions
One-off tasks should be created/tracked in the **inbox** container.

## Finding Tasks
```bash
wrkq find --state open --json
wrkq find 'myproject/api-feature/**' --state open
wrkq find --slug-glob 'login-*'
wrkq tree myproject --json
wrkq tree --json         # Show all tasks including completed
```

## Reading Tasks
```bash
# Show task details as markdown with metadata as frontmatter (includes comments by default)
wrkq cat T-00001

# Output as JSON (includes comments by default)
wrkq cat T-00001 --json

# List tasks in a path
wrkq ls myproject/api-feature --json
```

## Creating Tasks

```bash
# Create with title and description
wrkq touch myproject/feature/task-slug --state open --priority 2 -t "New Task" -d "Description"
# Create and emit JSON for scripting
wrkq touch myproject/feature/task-slug -t "New Task" -d "Description" --json
```


## Updating Tasks

```bash
# Set task state/priority/fields (quick updates)
wrkq set T-00001 --state in_progress
wrkq set T-00001 --title "New title"
wrkq set T-00001 --description "New description text"
wrkq set T-00001 --state in_progress --priority 1 --description "Starting work"

# Supported states: draft, open, in_progress, completed, blocked, cancelled, archived, deleted
# Priority: 1-4 (1 is highest)
# Supported fields: state, priority, title, slug, labels, due_at, start_at, description, kind, assignee, requested_by, assigned_project, resolution, cp_project_id, cp_run_id, cp_session_id, sdk_session_id, run_status
# Kind: task, subtask, spike, bug, chore
# Resolution: done, wont_do, duplicate, needs_info
# Run status: queued, running, completed, failed, cancelled, timed_out
```

## Comments
```bash
wrkq comment add T-00001 -m "Starting implementation at 10:00am"
```

## History
```bash
# Show task history
wrkq log T-00001 --oneline
wrkq log T-00001 --patch      # Show detailed changes
```

## Output Formats
Most commands support:
- `--json` - Pretty JSON
- `--ndjson` - Newline-delimited JSON (best for parsing)
- `--porcelain` - Stable machine-readable

</task_tracking_rules>
