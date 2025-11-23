# wrkq CLI - Agent Usage Guide
Quick reference for the most important wrkq commands when managing tasks.

## Task Management Best Practices

### Task Repository Conventions
1. **Use a top-level container for your project (e.g. `myproject`).**
2. **Use subdirectories for major features. (e.g. `api-authentication`)**
3. **Use short, descriptive slugs for tasks. (e.g. `login-auth-flow`, `logout-auth-flow`)**


### Task Lifecycle
1. **Before starting**: Set task to `in_progress`
   ```bash
   wrkq set T-00001 state=in_progress
   ```

2. **During work**: Add progress comments for significant milestones
   ```bash
   wrkq comment add T-00001 -m "Implemented core logic in cmd/apply.go"
   wrkq comment add T-00001 -m "Added test coverage, 3 edge cases found"
   ```

3. **Before completing**: Add final summary comment
   ```bash
   wrkq comment add T-00001 -m "Completed. Added apply cmd with 3-way merge support. Updated docs. All tests passing."
   wrkq set T-00001 state=completed
   ```

### When to Comment
- ✅ Key implementation decisions
- ✅ Blockers encountered and resolved
- ✅ Final summary before closing

### Task Hygiene
- Always set `state=in_progress` when you start
- Don't leave tasks `in_progress` when done
- Use `state=blocked` if waiting on something



## Finding Tasks
```bash
# Find all open tasks
wrkq find --state open --json

# Find tasks in specific path
wrkq find 'myproject/api-feature/**' --state open

# Find by slug pattern
wrkq find --slug-glob 'login-*'

# Tree view (hides completed/archived by default)
wrkq tree
wrkq tree --all --json          # Show all including completed
```

## Reading Tasks

```bash
# Show task details as markdown
wrkq cat T-00001

# List tasks in a path
wrkq ls myproject/api-feature --json
```

## Creating Tasks

```bash
# Create a task
wrkq touch myproject/api-feature/feature-name -t "Feature title"

# Task slug auto-normalized to lowercase a-z0-9-
```


## Deleting Tasks

```bash
# Delete a task
wrkq rm myproject/api-feature/feature-name

# Delete a task and all its attachments (interactive if --yes is not provided)
wrkq rm myproject/api-feature/feature-name --purge --yes
```

## Updating Tasks

### Task Metadata (CLI)
```bash
# Set task state/priority/fields (quick updates)
wrkq set T-00001 state=in_progress
wrkq set T-00001 state=completed priority=1
wrkq set T-00001 title="New title" due_at=2025-12-01

# Supported states: open, in_progress, completed, blocked, cancelled
# Priority: 1-4
```

### Task Description (MCP Tool - Claude Code Only)
**IMPORTANT**: In Claude Code, use the MCP tool to update task descriptions:

```
# Preferred method for Claude Code agents
mcp__wrkq__wrkq_update_description(taskId="T-00001", taskDescription="Updated task description...")

# Accepts: friendly IDs (T-00001), UUIDs, or paths (project/task-slug)
```

## Comments

```bash
# Add progress comment
wrkq comment add T-00001 -m "Starting implementation"

# Add with structured metadata for agents
wrkq comment add T-00001 -m "Analysis complete" --meta '{"findings":3}'
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

## Common Workflow

```bash
# 1. Find next task
wrkq find --state open --json | jq -r '.[0].id'

# 2. Start work
wrkq set T-00001 state=in_progress

# 3. Add progress comment
wrkq comment add T-00001 -m "Implemented core logic"

# 4. Complete
wrkq set T-00001 state=completed
```