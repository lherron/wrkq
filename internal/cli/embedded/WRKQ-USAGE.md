<task_tracking_rules>
# wrkq Task Management CLI

## Task Lifecycle
1. **Before starting a task**: Set task to `in_progress`
   ```bash
   wrkq set T-00001 --state in_progress
   ```

2. **During work on a task**: Add progress comments for significant milestones
   ```bash
   wrkq comment add T-00001 -m "Implemented core logic in cmd/apply.go"
   wrkq comment add T-00001 -m "Added test coverage, 3 edge cases found"
   ```

3. **Before completing a task**: Add final summary comment
   ```bash
   wrkq comment add T-00001 -m "Completed. Added apply cmd with 3-way merge support. Updated docs. All tests passing."
   wrkq set T-00001 --state completed
   ```

## TodoWrite tasks
Update the wrkq task **before** using the TodoWrite tool. When using the TodoWrite tool, include the wrkq task id in parenthesis whenever possible.

### Naming Conventions
1. **Use a top-level container for your project (e.g. `myfeat`).**
2. **Use subdirectories for major features. (e.g. `api-authentication`)**
3. **Use short, descriptive slugs for tasks. (e.g. `login-auth-flow`, `logout-auth-flow`)**

One-off tasks should be created/tracked in the **inbox** container.

## Managing Containers
```bash
# Create a directory container
wrkq mkdir myfeat

# Create a subcontainer with parents
wrkq mkdir -p myfeat/api-feature

# Remove an empty container
wrkq rmdir myfeat
```

### Global --project Flag
Use `--project` to override the default project for any command:
```bash
wrkq ls --project agent-spaces inbox          # List tasks in agent-spaces/inbox
wrkq cat --project webwrkq T-00001             # View task from webwrkq project
wrkq find --project myproject --state open     # Find open tasks in myproject
wrkq touch --project demo inbox/task -t "New"  # Create task in demo/inbox
```

### View All Projects
```bash
wrkq projects --json             # List all available projects
```

## Finding Tasks
```bash
# Find all open tasks
wrkq find --state open --json
wrkq find 'myfeat/api-feature/**' --state open
wrkq find --slug-glob 'login-*'
wrkq find --sort updated_at --reverse --limit 5
wrkq search 'query text' --ndjson
wrkq search 'query text' --sort updated_at --reverse --limit 5
wrkq tree myfeat --json
wrkq tree --json         # Show all tasks including completed
```

## Reading Tasks
```bash
# Show task details as markdown with metadata as frontmatter (includes comments by default)
wrkq cat T-00001

# Output as JSON (includes comments by default)
wrkq cat T-00001 --json

# List tasks in a path
wrkq ls myfeat/api-feature --json
wrkq ls myfeat/api-feature --type t --sort updated_at --reverse --limit 5
```

## Creating Tasks

```bash
# Create with title and a multi-line description via heredoc on stdin
wrkq touch myfeat/feature/task-slug --state open --priority 2 -t "New Task" -d - <<'EOF'
Multi-line description goes here.

- bullet one
- bullet two
EOF

# Same pattern works for --specification -
# Use <<'EOF' (quoted) to prevent shell expansion of $vars/backticks; drop quotes to allow it.

# Emit JSON for scripting
wrkq touch myfeat/feature/task-slug -t "New Task" -d - --json <<'EOF'
Description
EOF
```


## Deleting Tasks

```bash
# Delete a task
wrkq rm myfeat/api-feature/feature-slug

# Delete a task and all its attachments (interactive if --yes is not provided)
wrkq rm myfeat/api-feature/feature-slug --purge --yes
```

## Updating Tasks

### Task Metadata and Description (wrkq set)
```bash
# Set task state/priority/fields (quick updates)
wrkq set T-00001 --state in_progress
wrkq set T-00001 --title "New title" --due-at 2025-12-01
wrkq set T-00001 --description "New description text"

# Supported states: open, in_progress, completed, blocked, cancelled
# Priority: 1-4
# Supported fields: state, priority, title, slug, labels, due_at, start_at, description, specification


# Update multiple fields at once
wrkq set T-00001 --state in_progress --priority 1 --description "Starting work"

# Conditional update (only if etag matches)
wrkq set T-00001 --description "New text" --if-match 5
```

## Comments

```bash
# Add progress comment
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
