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
1. **Use a top-level container for your project (e.g. `myproject`).**
2. **Use subdirectories for major features. (e.g. `api-authentication`)**
3. **Use short, descriptive slugs for tasks. (e.g. `login-auth-flow`, `logout-auth-flow`)**

One-off tasks should be created/tracked in the **inbox** container.

### Agent Feature Requests
Coding agents should log any requests for new features or improvements to the task repository in **inbox/agent-feature-requests**.  (Create the container if it doesn't exist.)

Example:  If Claude Code tries to run **wrkq tree --json** and that flag isn't implemented, file a feature request to have it added in future.  Be sure to add a description of the feature request and the reason why it's needed.
  ```bash
  wrkq touch inbox/agent-feature-requests/workq-tree-json-flag -t "Add --json flag to wrkq tree command"

  # Then use wrkq tool to add a comment to the feature request
  ```

## Managing Containers
```bash
# Create a container
wrkq mkdir myproject

# Create a subcontainer with parents
wrkq mkdir -p myproject/api-feature

# Remove an empty container
wrkq rmdir myproject
```

## Finding Tasks
```bash
# Find all open tasks
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
```


## Deleting Tasks

```bash
# Delete a task
wrkq rm myproject/api-feature/feature-slug

# Delete a task and all its attachments (interactive if --yes is not provided)
wrkq rm myproject/api-feature/feature-slug --purge --yes
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
# Supported fields: state, priority, title, slug, labels, due_at, start_at, description


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
