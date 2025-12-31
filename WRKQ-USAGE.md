<task_tracking_rules>
# wrkq Cross-Agent Task Coordination

## Purpose
wrkq is the shared, offline task ledger. Control-plane (CP) is the system of record and serves
cross-project APIs. Use wrkq to coordinate work between agents/projects with minimal context.

## Canonical DB + Project Root Rules
- **Canonical DB**: set `WRKQ_DB_PATH` to the shared DB (ex: `~/.rex/projects-wrkq.db`).
- **Project root scoping**: set `WRKQ_PROJECT_ROOT` for CLI usage in a given project.
  - Paths are **project-relative** (do not include the projectId in slugs).
  - CP uses project-relative paths in `/admin/tasks` responses.
- **CLI loads `.env.local`** in the current working directory; run wrkq from the project root you intend.

## Cross-Project Handoff Workflow
1. **Create** task in `inbox/` for the target project.
2. **Annotate** with a short context comment (intent, links, assumptions).
3. **Start work**: set `in_progress` when an agent begins.
4. **Finish**: add a final summary comment and set `completed`.

## Minimal CLI Cheat Sheet
```bash
# See what needs attention
wrkq check-inbox

# Create a task
wrkq touch inbox/<slug> -t "Title" -d "Short description"

# Comment on progress or handoff context
wrkq comment add T-00001 -m "Context or update"

# Update state
wrkq set T-00001 --state in_progress
wrkq set T-00001 --state completed

# Read task details + comments
wrkq cat T-00001
```

## Troubleshooting (Cross-Comms)
- **Task not visible in another project**:
  - Confirm `WRKQ_DB_PATH` points to the canonical DB.
  - Confirm `WRKQ_PROJECT_ROOT` is set for the target project.
  - Ensure CP is running and using the same canonical DB.
- **Containers tree empty in UI**:
  - Hit `/admin/tasks/containers/tree` once; CP will auto-seed project roots/inboxes.

## Agent Feature Requests
Log CLI gaps in `inbox/agent-feature-requests`.
Example:
```bash
wrkq touch inbox/agent-feature-requests/<slug> -t "Feature request title" -d "Why it matters"
```
</task_tracking_rules>
