# Refactoring Recommendations (2025-12-03)

## High-priority (foundation)
1) CLI bootstrap/context ✅ (partial - infrastructure in place)
- Problem: Every command redoes `config.Load` + DB open and often actor resolution (35 occurrences of `config.Load` across `internal/cli`). Error text in `resolveCurrentActor` still refers to `TODO_ACTOR`.
- Proposal: Add `internal/cli/appctx` with a shared initializer that loads config (honoring `--db`), opens the DB once, resolves actor (when needed), and injects a small `App` struct into command handlers (`withApp(run func(ctx *App, cmd *cobra.Command, args []string) error)`). Close DB in a centralized `PostRun`. This removes boilerplate and aligns error handling/exit codes.
- **Status**: `internal/cli/appctx` created with `App`, `Options`, `Bootstrap()`, `WithApp()`. Error messages fixed to use `WRKQ_ACTOR`/`WRKQ_ACTOR_ID`. Sample commands (`cat.go`, `touch.go`) migrated. Remaining commands can be migrated incrementally.

2) Path/selector reuse for containers and tasks  
- Problem: Container/task traversal is reimplemented in `mkdir.go`, `touch.go`, `ls.go`, `tree.go`, `mv.go`, `cp.go`, etc., while `selectors.ResolveTask` already exists. Slug normalization (`paths.NormalizeSlug`) is scattered (16+ sites) with slightly different error messages.  
- Proposal: Extend `internal/selectors` (or a new `paths/resolver`) with `ResolveContainerPath`/`ResolveTaskPath` that walk the hierarchy once (optionally create parents for mkdir/touch). Have all commands call these helpers instead of rolling SQL, so slug rules and errors stay consistent.

3) Store/service layer with baked-in events and etags  
- Problem: Raw SQL lives in CLI files (e.g., `set.go` builds `UPDATE` strings, `touch.go` and `mkdir.go` assemble inserts, `attach.go` writes attachments) and each site separately increments `etag` and crafts event payload JSON. Behavior drift is likely and transactions are verbose.  
- Proposal: Introduce `internal/store` with submodules (`Tasks`, `Containers`, `Attachments`, `Comments`) that expose typed methods: `Create`, `UpdateFields`, `Move`, `List`, etc., all handling etag bumps, timestamps, and event writes internally. Commands then call store methods, shrinking run functions and making changes auditable in one place.

4) Cursor pagination helper wired into SQL  
- Problem: `ls`, `attach ls`, `comment ls`, `log`, and `find` decode cursors but still fetch all rows and slice in memory; `cursor.BuildWhereClause` is tested yet unused. Large datasets will degrade quickly.  
- Proposal: Add a small helper (`cursor.Apply(query, c, limit, orderBy, descending)`) that injects the `WHERE` from `BuildWhereClause`, appends `ORDER BY`/`LIMIT`, and returns the next cursor. Replace the ad-hoc filtering blocks to make pagination consistent and cheap.

## Medium-priority cleanups
5) Shared admin vs user command wiring  
- Problem: `root.go` vs `rootadm.go` and `version.go` vs `versionadm.go` duplicate flag setup and JSON payloads. Divergence risk is high as commands are added.  
- Proposal: Provide factory functions (`newRootCmd(binary string, admin bool)`, `newVersionCmd(binary string, supported []string)`) and register the same flag set. Keep the supported-command lists in a single source to avoid skew.

6) Validation and parsing utilities  
- Problem: Slug rules live in `paths`, state/priority in `domain`, labels/time parsing are repeated, and `actors.parseTime` is a private copy. Error strings differ across commands.  
- Proposal: Consolidate into `internal/domain/validate` (slugs, labels JSON, RFC3339 timestamps, actor env resolution) and have callers reuse them. Fix the env hint in `resolveCurrentActor` to reference `WRKQ_ACTOR` / `WRKQ_ACTOR_ID`.

7) Event payload builders  
- Problem: Many commands hand-roll JSON strings for events (`touch`, `mkdir`, `attach`, `set`), so payload shapes are inconsistent.  
- Proposal: Add lightweight payload helpers (`events/payload.go`) or embed them in the new store so event bodies are structs marshalled once, keeping telemetry consistent and reducing copy/paste.

Notes / quick wins: Remove or wire the unused `exitError` helper; prefer the shared bootstrap for DB/config in new code paths; keep `cursor` helpers covered by existing tests when you wire them in.
