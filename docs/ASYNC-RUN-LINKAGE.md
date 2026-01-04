# Async Run Linkage (CP ↔ wrkq)

## Goal
Persist minimal Control Plane (CP) async run linkage on wrkq tasks so webwrkq can
update status without parsing comments.

## Task Fields (current run only)
Add nullable columns on `tasks`:
- `cp_project_id` TEXT
- `cp_run_id` TEXT
- `cp_session_id` TEXT
- `sdk_session_id` TEXT
- `run_status` TEXT CHECK (run_status IN ('queued','running','completed','failed','cancelled','timed_out'))

Notes:
- `cp_run_id` is current-only (overwritten by latest async run).
- `cp_project_id` is async-only and optional (some tasks are not project-linked).
- Timestamps are deferred to a later change.

## Migrations
- Add new SQL migration under `internal/db/migrations/`.
- Update `schema_dump.sql`.
- Optional indexes if needed for lookups: `cp_run_id`, `cp_session_id`.

## CLI Updates (wrkq set)
Extend `wrkq set` flags to write the new fields:
- `--cp-project-id <id>` → `cp_project_id`
- `--cp-run-id <id>` → `cp_run_id`
- `--cp-session-id <id>` → `cp_session_id`
- `--sdk-session-id <id>` → `sdk_session_id`
- `--run-status <queued|running|completed|failed|cancelled|timed_out>` → `run_status`

Validate `run_status` against the enum list.

## wrkqd (daemon)
If daemon API parity is needed, extend `handleTasksUpdate` to accept the new keys
and persist them (same column mapping as `wrkq set`).

## Control Plane (CP)
No new CP endpoints. Extend the existing PATCH schema to accept the new fields
and pass them through to `wrkq set`.

## webwrkq async flow
After run creation:
- PATCH task with `cp_project_id`, `cp_run_id`, `cp_session_id`, `run_status='queued'`.

During polling:
- PATCH `run_status` as it changes.
- PATCH `sdk_session_id` once available.

## Canonical DB Migration
Run `wrkqadm migrate` against canonical DB. No backfill required.
