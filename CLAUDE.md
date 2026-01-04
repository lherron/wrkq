# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**wrkq** is a filesystem-flavored CLI for managing projects, subprojects, and tasks on a SQLite backend. It feels like Unix utilities (`ls`, `cat`, `mv`, `rm`, `tree`, `touch`, `mkdir`) and is designed for collaboration between humans and coding agents.

The system ships **two binaries**:
- **`wrkq`** - Agent + human collaboration surface (task/container/comment CRUD, content editing, history)
- **`wrkqadm`** - Administrative surface (database lifecycle, actor management, bundle application, health checks)

This separation ensures agents get a focused, safe API while admins retain full control over database lifecycle and infrastructure concerns.

**We dogfood wrkq**: This project uses wrkq to manage its own development tasks. The wrkq database tracks active work, and agents working on this codebase should use wrkq commands to view, update, and comment on tasks.

## WRKQ Usage Reference

** ALWAYS USE WRKQ TO TRACK YOUR TASK **

Run `wrkq info` for instructions on using wrkq.

## Development Commands

### Build & Test
```bash
# Build both binaries (wrkq and wrkqadm)
just build
# Or explicitly:
go build -o bin/wrkq ./cmd/wrkq
go build -o bin/wrkqadm ./cmd/wrkqadm

# Run all tests
just test
# Or:
go test -v ./...

# Run tests with coverage
just test-coverage

# Format code
just fmt

# Lint code
just lint

# Verify all (lint + test)
just verify

# Pre-commit checks (fmt + lint + test)
just pre-commit

# Quick smoke test (build + test)
just smoke
```

### Install & Run
```bash
# Install both binaries to ~/.local/bin
just install

# Run wrkq directly (without installing)
just run <args>
# Or:
go run ./cmd/wrkq <args>

# Run wrkqadm directly
just wrkqadm-run <args>
# Or:
go run ./cmd/wrkqadm <args>
```


## Architecture

### Binaries
- **`cmd/wrkq/main.go`** - Entry point for wrkq CLI (calls `cli.Execute()`)
- **`cmd/wrkqadm/main.go`** - Entry point for wrkqadm CLI (calls `cli.ExecuteAdmin()`)

### Core Packages

#### `internal/cli/`
Contains all command implementations for both binaries:
- **`root.go`** - Root command for wrkq (registers all wrkq subcommands)
- **`rootadm.go`** - Root command for wrkqadm (registers all admin subcommands)
- Command files use Cobra framework (`github.com/spf13/cobra`)
- All commands respect global flags: `--db` (database path), `--as` (actor override)

#### `internal/db/`
- **`db.go`** - SQLite connection wrapper, migration runner
- **`migrations/*.sql`** - SQL migration files (embedded at compile time)
- Migrations are embedded from `internal/db/migrations/*.sql` using `//go:embed`
- Migration tracking: `schema_migrations` table records applied migrations by filename
- Key methods: `Migrate()`, `MigrateWithInfo()`, `MigrationStatus()`
- Database setup:
  - WAL mode enabled
  - Foreign keys enforced
  - Busy timeout: 5000ms
  - Synchronous: NORMAL

#### `internal/domain/`
Core domain types:
- **`types.go`** - Defines all domain models (Actor, Container, Task, Comment, Attachment, Event)
- **`validation.go`** - Slug normalization and validation rules
- Slug rules: lowercase `[a-z0-9-]`, must start with `[a-z0-9]`, max 255 bytes, unique among siblings

#### `internal/config/`
Configuration loading with precedence:
1. CLI flags
2. Environment variables (`WRKQ_DB_PATH`, `WRKQ_ACTOR`, etc.)
3. `./.env.local` (dotenv)
4. `~/.config/wrkq/config.yaml`

#### `internal/actors/`
Actor resolution logic:
1. `--as <actor>` flag (accepts slug or friendly ID like `A-00001`)
2. `WRKQ_ACTOR_ID` env (friendly ID)
3. `WRKQ_ACTOR` env (slug)
4. `default_actor` in config
5. Fallback to seeded default (e.g., `local-human`)

#### `internal/paths/`
- **`glob.go`** - Pathspec and glob resolution (supports `*`, `?`, `**`)
- **`slug.go`** - Slug normalization and validation

#### `internal/selectors/`
Resource selector parsing:
- Container paths: `project/subproject/task`
- Friendly IDs: `P-00007`, `T-00123`, `A-00001`, `C-00012`
- UUIDs: standard UUID format
- Typed selectors: `t:<token>` (task), `c:<token>` (comment)

#### `internal/render/`
Output formatting (supports `--json`, `--ndjson`, `--yaml`, `--tsv`, `--porcelain`)

#### `internal/edit/`
3-way merge logic for `wrkq edit` and `wrkq apply --base`

#### `internal/attach/`
Attachment path resolution and I/O. Canonical directory: `attach_dir/tasks/<task_uuid>/...`

#### `internal/events/`
Event log write/read helpers (append-only audit trail)

#### `internal/bundle/`
Bundle create/apply logic for Git-ops workflow (shared by both binaries)

#### `internal/bulk/`, `internal/cursor/`
Bulk operations and pagination support

#### `internal/id/`
Friendly ID generation (e.g., `T-00123`, `P-00007`)

#### `internal/testutil/`
Test utilities and helpers

## Data Model

### Core Resources
- **Actor** - Human, agent, or system entity (has UUID, friendly ID, slug, role)
- **Container** - Project or subproject (hierarchical via `parent_uuid`)
- **Task** - Actionable item under a container (has slug, title, state, priority, description, labels)
- **Comment** - Immutable note on a task (append-only, soft-deletable)
- **Attachment** - File reference (metadata in DB, bytes on filesystem)
- **Event** - Audit log entry (resource_type, event_type, payload)

### Concurrency Model
- Every mutable row has `etag INTEGER` (increments on write)
- All mutating commands support `--if-match <etag>` for optimistic concurrency
- Conflicts exit with code `4`
- WAL mode + busy_timeout enables concurrent CLI/agent access

### Task States
- `open` - Not started
- `in_progress` - Currently being worked on
- `completed` - Done
- `blocked` - Waiting on something
- `cancelled` - Not doing
- `archived` - Soft-deleted (via `archived_at` timestamp)

## Key Conventions

### Addressing
Resources can be referenced by:
- Path: `project/subproject/task`
- Friendly ID: `T-00123`
- UUID: `2fa0a6d6-3b0d-4b3b-9fdd-4bb0d5e6a7c1`
- Typed selector: `t:T-00123`, `c:C-00012`

### Globbing
- Patterns: `*`, `?`, `**`
- Example: `wrkq ls 'portal/**/login-*' -type t`
- Always quote patterns to avoid shell expansion

### Output Formats
Most commands support:
- `--json` - Pretty JSON
- `--ndjson` - Newline-delimited JSON (best for piping)
- `--yaml` - YAML
- `--tsv` - Tab-separated values
- `--porcelain` - Stable machine-readable (no ANSI, stable keys)

### Exit Codes
- `0` - Success
- `1` - Generic error (db, io)
- `2` - Usage error (bad flags, args)
- `3` - Not found (no matches)
- `4` - Conflict (etag mismatch, merge conflict)
- `5` - Partial success (with `--continue-on-error`)

## Testing Patterns

### Unit Tests
- Test files: `*_test.go` alongside implementation
- Use `internal/testutil` for common test helpers
- Run specific package tests: `go test -v ./internal/paths`

### Integration Tests
- See `internal/cli/integration_test.go` for examples
- Tests create temporary databases and exercise full command flows
- Use `t.TempDir()` for isolated test databases

### Smoke Tests
- Shell scripts in `test/` directory
- `smoke-m1.sh`, `smoke-m5.sh`, `smoke-wrkqadm.sh`
- Test full workflows end-to-end

## Common Development Tasks

### Adding a New Command
1. Create command file in `internal/cli/` (e.g., `mycommand.go`)
2. Implement as Cobra command
3. Register in `init()` of either `root.go` (for wrkq) or `rootadm.go` (for wrkqadm)
4. Add tests in `mycommand_test.go`

### Adding a Migration
1. Create new file in `internal/db/migrations/` with sequential number: `000007_description.sql`
2. Migrations are auto-embedded via `//go:embed` and shipped with the binaries
3. Applied migrations are tracked in the `schema_migrations` table (version + applied_at timestamp)
4. Run `wrkqadm migrate` to apply pending migrations, or use `wrkqadm init` which runs migrations automatically

### Database Versioning
- Migrations use the `schema_migrations` table (not SQLite's `pragma user_version`)
- Each migration file (e.g., `000001_baseline.sql`) is recorded by filename when applied
- Check migration status: `wrkqadm migrate --status`
- Preview pending migrations: `wrkqadm migrate --dry-run`
- Apply migrations: `wrkqadm migrate` (idempotent, safe to run multiple times)

## Important Notes

### Slug Normalization
- Slugs are always lowercase `[a-z0-9-]`
- Must start with `[a-z0-9]` (not hyphen)
- Max 255 bytes
- Unique among siblings
- Validation is in `internal/domain/validation.go`

### Actor Attribution
- All mutating commands require an actor
- Use `--as` flag to override
- Agents should set `WRKQ_ACTOR=agent-name` in their environment
- No authentication, only attribution for provenance

### Attachments
- Metadata in database
- Files stored at `attach_dir/tasks/<task_uuid>/...`
- Task moves don't affect attachment paths (keyed by UUID)
- Soft delete preserves files; `--purge` removes directory

### Comments
- Soft delete via `deleted_at` timestamp
- Hard delete with `--purge` flag
- Comments block in `wrkq cat --include-comments` is read-only (not parsed by `apply`)

## Specification Documents

- `docs/SPEC.md` - Full product specification (source of truth for behavior)
- `docs/WRKQADM.md` - Binary separation rationale
- `docs/ER.md` - Entity-relationship documentation
- `WRKQ-USAGE.md` - Quick reference for agents
