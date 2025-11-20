# CLAUDE.md


## Temp files
Temporary files in Bash tool calls should use ./tmp (from project root) NOT /tmp

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`wrkq` is a Unix-style CLI for managing projects, subprojects, and tasks backed by SQLite. It follows filesystem-flavored UX patterns (think `ls`, `cat`, `mkdir`, `mv`, `rm`) and is designed for both human users and coding agents as collaborators. All mutations are attributed to actors and tracked in an append-only event log.

**Key principle**: Pipe-friendly, machine-first outputs with stable contracts to support future browser UI.

## Build and Test Commands

```bash
# Build
just cli-build              # Build wrkq to ./bin/wrkq
just wrkqadm-build          # Build wrkqadm to ./bin/wrkqadm
just install                # Install both binaries to ~/.local/bin/

# Development
just cli-run <args>         # Run wrkq without building (go run)
just cli-fmt                # Format Go code
just cli-lint               # Lint with golangci-lint

# Testing
just cli-test               # Run all tests
just smoke                  # Quick smoke test (build + test)
./test/smoke-m1.sh          # M1 feature smoke tests
./test/smoke-wrkqadm.sh     # wrkqadm smoke tests

# Single test
go test -v -run TestSomething ./internal/cli

# Test with coverage
just cli-test-coverage      # Generates coverage.html
```

## Environment Configuration

The CLI uses multi-source configuration with this precedence (highest to lowest):

1. CLI flags: `--db`, `--as`
2. Environment variables: `WRKQ_DB_PATH`, `WRKQ_ACTOR`
3. `.env.local` file (for development, gitignored)
4. `~/.config/wrkq/config.json`

**For development**, use `.env.local`:
```bash
WRKQ_DB_PATH=/tmp/claude/test-wrkq.db
WRKQ_ACTOR=local-human
```

This avoids needing `--db` flags in every command during development.

## Architecture

### Database Layer (`internal/db`)

- **SQLite with WAL mode**: All data in single file, concurrent-safe
- **Embedded migrations**: SQL files in `internal/db/migrations/*.sql`, applied via `db.Migrate()`
- **Pragmas**: `foreign_keys=ON`, `journal_mode=WAL`, `busy_timeout=5000`
- **Type**: `*db.DB` wraps `*sql.DB` and adds `Path()` method

Always use `db.Open(cfg.DBPath)` to get a database handle. Never use `sql.Open()` directly.

### Command Pattern (`internal/cli`)

Each CLI command follows this structure:

1. **Command definition**: Cobra command in `internal/cli/<name>.go`
2. **Flag parsing**: Flags defined in `init()` function
3. **Config loading**: `config.Load()` for multi-source config
4. **Database access**: `db.Open(cfg.DBPath)` with proper cleanup
5. **Resource resolution**: Use existing helpers like `resolveTask()`, `resolveContainer()`
6. **Output rendering**: Use `internal/render` for JSON/NDJSON/table/porcelain

**Key pattern for database access**:
```go
database, err := db.Open(cfg.DBPath)
if err != nil {
    return fmt.Errorf("failed to open database: %w", err)
}
defer database.Close()
```

Commands are auto-registered via `init()` with `rootCmd.AddCommand()`.

### Actor Resolution (`internal/actors`)

All mutations must be attributed to an actor. Resolution order:

1. `--as` flag
2. `WRKQ_ACTOR_ID` env (UUID)
3. `WRKQ_ACTOR` env (slug)
4. Config file `actor_id` or `actor_slug`

Use the actors package to resolve current actor before mutations.

### Resource Identification

Resources are identified via three mechanisms:

1. **Friendly IDs**: `T-00001`, `P-00001`, `A-00001` (stable, sequential, human-readable)
2. **UUIDs**: Canonical database identifiers
3. **Paths**: Hierarchical slugs like `portal/auth/login-ux`

**Important**: Many commands share a `resolveTask()` helper (in `cat.go`) that returns `(uuid, friendlyID, error)`. Reuse this pattern for consistency.

### Concurrency Control

- **ETag-based optimistic locking**: Every mutable row has an `etag INTEGER` that increments on writes
- **`--if-match` flag**: Conditional updates based on etag value
- **Exit code 4**: Conflict (etag mismatch or merge conflict)
- **Transactions**: Critical mutations use transactions with etag checks

### Event Log

All mutations append to `event_log` table:
- Resource UUID and type
- Event type (e.g., `task.created`, `task.updated`)
- Actor UUID
- Resulting etag
- Optional JSON payload with deltas

Commands like `log` and `watch` read from this table.

### 3-Way Merge (`internal/edit`)

The `edit` command uses 3-way merge logic:

1. **Base**: Task state when editing began
2. **Current**: Task state in DB now (may have changed)
3. **Edited**: User's changes from $EDITOR

Auto-resolves when possible, marks conflicts otherwise. Returns `MergeResult` with `HasConflict` flag.

## Output Formats

All commands should support multiple output formats where applicable:

- **`--json`**: Pretty-printed JSON (use `render.RenderJSON()`)
- **`--ndjson`**: Newline-delimited JSON (use `render.RenderNDJSON()`)
- **`--porcelain`**: Stable, machine-readable (use `render.RenderTable(..., porcelain=true)`)
- **`-1`**: One per line
- **`-0`**: NUL-separated for `xargs -0`

Use helpers from `internal/render` package rather than manual encoding.

## Slug Normalization

All slugs (actors, containers, tasks) must follow strict rules:

- Lowercase only
- Allowed: `a-z`, `0-9`, `-`
- Must start with `[a-z0-9]`
- Max 255 bytes
- Regex: `^[a-z0-9][a-z0-9-]*$`

Use `paths.NormalizeSlug()` for validation and normalization. Never manually construct slugs.

## Common Patterns

### Creating a New Command

1. Create `internal/cli/<command>.go`
2. Define Cobra command with flags
3. Implement `run<Command>()` function following the pattern in existing commands
4. Register command in `init()` with `rootCmd.AddCommand()`
5. Handle config loading, DB access, resource resolution, output rendering
6. Add to smoke tests if it's a major feature

### Database Queries

Prefer prepared statements and always scan into typed structs:

```go
query := `SELECT uuid, id, slug, title FROM tasks WHERE uuid = ?`
var task struct {
    UUID  string
    ID    string
    Slug  string
    Title string
}
err := database.QueryRow(query, taskUUID).Scan(&task.UUID, &task.ID, &task.Slug, &task.Title)
```

### Error Handling

- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`
- Use specific exit codes:
  - 0: success
  - 1: generic error
  - 2: usage error (bad flags/args)
  - 3: not found
  - 4: conflict (etag mismatch, merge conflict)
  - 5: partial success (with `--continue-on-error`)

## Testing

### Smoke Tests

Located in `test/smoke-m1.sh`. These test all major commands end-to-end with a fresh database.

Run with: `./test/smoke-m1.sh`

### Unit Tests

Located alongside code in `internal/cli/*_test.go`:
- `integration_test.go`: Cross-command integration tests
- `concurrency_test.go`: Concurrent write tests
- `performance_test.go`: Performance benchmarks

### Test Database

Tests should use a temporary database:
```go
tmpDB := "/tmp/test-" + t.Name() + ".db"
defer os.Remove(tmpDB)
database, err := db.Open(tmpDB)
```

## Milestones

- **M0 (Complete)**: Core CRUD commands, SQLite backend, friendly IDs, event log, actor attribution
- **M1 (Complete)**: `edit`, `apply`, `diff`, `log`, `watch`, `find`, `tree` commands with 3-way merge
- **M2 (Next)**: Attachments, pagination, bulk ops, `cp` command, GoReleaser packaging

See `M0-SUMMARY.md` and `M1-SUMMARY.md` for detailed implementation notes.

## Dogfooding: Using `wrkq` for its own development

**This project uses itself for development tracking.** All M2+ development is tracked in the `wrkq` CLI itself, not TODO.md.
Mark tasks as in_progress when you start a task and done when you complete it.
When completing a task, add a wrkq comment.
When you use the TodoWrite tool, reference the wrkq ID when one exists

### Updating Tasks via MCP

When you need to update wrkq task body content, use the `wrkq_task_write` MCP tool instead of manual file editing or CLI commands:

```
# Instead of manually editing with wrkq edit or wrkq apply
# Use the MCP tool directly:
mcp__wrkq__wrkq_task_write(taskId="T-00024", taskBody="...")
```

**When to use the MCP tool:**
- Adding or updating task specifications
- Writing implementation notes to tasks
- Updating task body content with research findings
- Any time you need to modify task body text

**Benefits:**
- Direct integration from Claude Code
- No need for temp files or manual apply commands
- Cleaner workflow within conversations

### Initial Setup



If the `todo/` project structure doesn't exist yet, run the migration:

```bash
./scripts/migrate-wrkq.sh
```

This creates:
- `wrkq/m0/` - M0 completed tasks
- `wrkq/m1/` - M1 completed/pending tasks
- `wrkq/m2/` - M2 upcoming tasks
- `wrkq/meta/` - Meta tasks about the project

### Common Workflows

**See what's next:**
```bash
wrkq tree wrkq                           # Big picture (hides completed/archived by default)
wrkq tree wrkq --all                     # Show all tasks including completed/archived
wrkq find --state open                   # All open tasks
wrkq find 'wrkq/m2/**' --state open      # M2 tasks only
```

**Note:** `wrkq tree` hides completed and archived items by default to keep the view focused on active work. Use the `--all` flag to see everything.

**Start working on a task:**
```bash
# Find the task
wrkq find --slug-glob 'attachments*'

# Start work
wrkq set T-00015 state=in_progress

# Add implementation notes
EDITOR=vim todo edit T-00015
```

**Track progress:**
```bash
# See what you've completed today
wrkq find --state completed --json | \
  jq -r '.[] | select(.updated_at >= "'$(date -I)'") | "\(.id) \(.title)"'

# Watch real-time updates
wrkq watch --follow --ndjson | \
  jq -r '"\(.timestamp) - \(.event_type) - \(.resource_id)"'

# Check milestone progress
TOTAL=$(todo find 'wrkq/m2/**' --type t --json | jq 'length')
DONE=$(todo find 'wrkq/m2/**' --type t --state completed --json | jq 'length')
echo "M2 Progress: $DONE/$TOTAL"
```

**Complete a task:**
```bash
wrkq set T-00015 state=completed

# Review what changed
wrkq log T-00015 --oneline
```

### Recommended Aliases

Add to `.bashrc` or `.zshrc`:

```bash
# Quick views
alias tl='wrkq tree todo'                    # Overview
alias tn='wrkq find --state open'            # Next tasks
alias td='wrkq find --state completed'       # Done
alias tw='wrkq watch --follow --ndjson'      # Watch

# Quick actions
alias te='EDITOR=vim wrkq edit'              # Edit task
alias ts='wrkq set'                          # Set field
```

### Creating New Tasks

When implementing a new feature:

```bash
# Create the task
wrkq touch wrkq/m2/new-feature -t "Implement new feature"

# Set metadata
wrkq set wrkq/m2/new-feature priority=1 state=open

# Add spec details
EDITOR=vim todo edit wrkq/m2/new-feature
```

### Querying Tasks

```bash
# High priority items
wrkq find --json | jq -r '.[] | select(.priority <= 2) | "\(.id) \(.title)"'

# Blocked tasks
wrkq find --state blocked

# Recently updated
wrkq find --json | jq -r 'sort_by(.updated_at) | reverse | .[0:5] | .[] | "\(.id) \(.title)"'

# Generate release notes
wrkq find 'wrkq/m1/**' --state completed --json | \
  jq -r '.[] | "- \(.title)"'
```

### Benefits of Dogfooding

1. **Real validation** - Surfaces UX issues immediately
2. **Better prioritization** - Shows what M2/M3 should focus on
3. **Authentic examples** - Documentation from actual usage
4. **Confidence** - "We use what we build"

See `DOGFOOD.md` and `READY-TO-DOGFOOD.md` for complete dogfooding guide.

## Key Files

- `docs/SPEC.md`: Complete product specification (behavior, UX, data model)
- `docs/CLI-MVP.md`: Milestone breakdown (M0-M4)
- `TODO.md`: Implementation tracker (REMOVED - Use `wrkq` CLI)
- `DOGFOOD.md`: Guide to using `wrkq` for its own development
- `READY-TO-DOGFOOD.md`: Decision document on dogfooding
- `scripts/migrate-wrkq.sh`: Migrate TODO.md tasks to `wrkq` CLI
- `internal/db/migrations/`: SQL schema migrations
- `mcp-server/`: MCP server for AI assistant integration (see README.md)
- `.env.local`: Development environment config (gitignored)

## Special Considerations

### Sandbox Mode

When running bash commands for operations like builds, the sandbox may restrict file writes. If you see "Operation not permitted" errors:

1. Check if it's truly a sandbox issue (not missing files, wrong args, etc.)
2. If sandbox is blocking legitimate operations, retry with `dangerouslyDisableSandbox: true`
3. The `just cli-build` command typically requires sandbox disabled for Go build cache access

### Working with Tasks

When implementing features that work with tasks:

1. Always fetch the full `taskData` struct for operations needing multiple fields
2. Use the existing `resolveTask()` helper from `cat.go` to resolve IDs/paths to UUIDs
3. Remember that `body` is stored in the `body` column (not `description`)
4. Priority is 1-4 with database constraint checks
5. State is constrained to: `open`, `in_progress`, `completed`, `blocked`, `cancelled`

### Path Resolution

The codebase has two path concepts:

1. **Container paths**: Hierarchical like `portal/auth/login`
2. **Task paths**: Full path including task slug like `portal/auth/login-ux`

Path resolution uses recursive CTEs and the `container_paths` view. See `resolveTask()` in `cat.go` for the canonical implementation pattern.
