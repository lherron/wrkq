# wrkq Conceptual Domain Model

This document describes the conceptual domain model for wrkq - the business concepts, their meanings, and how they relate to each other in the problem domain. This is not a database schema; it's a model of the concepts users work with.

> **Implementation Status**: Most concepts are fully implemented in both schema and CLI. Exceptions are noted where applicable (e.g., Sections have schema support but no CLI commands yet).

---

## The Domain: Human-Agent Task Collaboration

wrkq is a collaboration surface where **humans** and **coding agents** work together on software projects. The core problem it solves: tracking work across multiple autonomous actors while maintaining clear attribution and preventing conflicts.

---

## Actors (Who Does the Work)

Work in wrkq is performed by actors. Each actor has a distinct identity and role.

### Human

A person interacting with the system. Humans typically:
- Create and organize projects
- Define tasks and acceptance criteria
- Review agent work
- Make strategic decisions

### Agent

An AI coding assistant (Claude, GPT, etc.) performing automated work. Agents:
- Execute tasks autonomously
- Report progress via comments
- Create subtasks as they discover work
- Operate within defined boundaries

### System

Internal processes that maintain the system:
- Database migrations
- Initialization routines
- Repair operations

---

## Organizational Structure

Work is organized hierarchically, mirroring how teams think about software projects.

### Project

A top-level organizational boundary. Projects are independent workstreams that don't share tasks. Examples:
- "customer-portal" - A web application
- "api-service" - A backend service
- "documentation" - Docs site

### Feature

A deliverable capability within a project. Features represent user-visible functionality that can be planned, worked on, and completed. Examples:
- "user-authentication" - Login/logout flow
- "payment-processing" - Checkout integration
- "search" - Full-text search capability

### Area

A cross-cutting concern that spans features. Areas group related work that doesn't fit neatly into a single feature. Examples:
- "performance" - Speed optimizations anywhere
- "security" - Security hardening across the system
- "tech-debt" - Refactoring and cleanup

### Miscellaneous Container

A catch-all for items that don't fit elsewhere. Used for:
- Temporary holding areas
- Exploratory work
- Uncategorized items

---

## Work Items

The actual units of work that get done.

### Task

A discrete piece of work that can be assigned, tracked, and completed. Tasks have:
- A clear definition of done
- An owner (assignee)
- A current state
- Priority relative to other tasks

### Subtask

A smaller piece of work that's part of a larger task. Subtasks:
- Cannot exist independently (always belong to a parent task)
- Cannot have their own subtasks (single level only)
- Share the context of their parent

### Spike

A time-boxed investigation to reduce uncertainty. Spikes:
- Answer a specific question
- Have a fixed duration
- Result in knowledge, not code
- Example: "Investigate auth library options"

### Bug

A defect that needs fixing. Bugs:
- Describe unexpected behavior
- Reference expected vs actual behavior
- May include reproduction steps

### Chore

Maintenance work that doesn't add features. Chores:
- Improve code quality
- Update dependencies
- Refactor without changing behavior
- Example: "Upgrade to React 19"

---

## Workflow States

Tasks move through states as work progresses.

### Idea

Pre-triage concept captured for later refinement. Ideas are intentionally excluded from default views until promoted to draft.

### Draft

Triage-ready work that is defined enough to evaluate but not yet committed to execution.

### Open

Work has been defined but not started. The task is in the backlog, waiting to be picked up.

### In Progress

Someone is actively working on this task. Only one actor should have a task in progress at a time.

### Blocked

Work cannot continue due to an external dependency. The blocking issue should be documented.

### Completed

The work is done and meets acceptance criteria. The task is closed but remains visible for history.

### Cancelled

The work will not be done. The reason should be documented. Different from "completed" - nothing was delivered.

### Archived (Visibility)

Not a workflow state, but a visibility filter. Archived items have an `archived_at` timestamp set and are hidden from default queries. Archiving preserves history while decluttering the active view.

---

## Collaboration

How actors communicate and coordinate.

### Comment

An immutable message attached to a task. Comments serve as the async communication channel between humans and agents. Once posted, comments cannot be edited (ensuring audit integrity).

Comments typically contain:
- Progress updates ("Implemented the login form")
- Questions ("Should this support SSO?")
- Blockers ("Waiting on API credentials")
- Completion notes ("Done. Added tests for edge cases.")

### Attachment

A file associated with a task. Attachments provide supporting materials:
- Screenshots of bugs
- Design mockups
- Log files
- Specification documents

---

## Handoffs (Agent Session Continuity)

A **handoff** is an intentional, manually-created context record that one session of an agent leaves behind for a later session of the same agent. Handoffs are a sibling collaboration primitive to tasks, comments, and memory — not a special task kind, not a comment, and not an auto-managed memory entry.

### Tasks vs Comments vs Memory vs Handoffs

These four primitives are deliberately distinct. Picking the wrong one causes coordination noise.

| Primitive | Audience | Lifecycle | Purpose |
|-----------|----------|-----------|---------|
| **Task** | Between actors (human↔agent, agent↔agent) | Workflow states (open → in_progress → completed/…) | Coordinate work that needs an owner, definition of done, and tracking |
| **Comment** | On a specific task, between actors collaborating on it | Append-only, immutable | Progress updates, questions, blockers, completion notes |
| **Memory** | Belongs to one agent | Auto-managed and auto-pruned | Background knowledge the agent has absorbed; not deliberately curated for a future session |
| **Handoff** | Same agent, current → future session | Created → pending → acknowledged (manual retirement only) | Carry deliberate, auditable context forward between the same agent's sessions |

A simple test: "Do I need someone else to do this?" → task. "Am I updating a task?" → comment. "Should the model just remember this in general?" → memory. "Do I, this same agent, want to pick this up next session with full context?" → handoff.

### Scope Model

Handoffs are scoped to an `(agent, project)` pair in v1. Task and lane variants are intentionally excluded for now; the schema's `scope_kind` column reserves space for future expansion without a migration.

Scope is identified by a validated **ScopeRef** drawn from the canonical `agent-scope` package (the TypeScript original lives in `agent-spaces/packages/agent-scope`; the Go port lives in `internal/scope/`). Two surface forms:

- **Long form (`scopeRef`)**: `agent:<agentId>:project:<projectId>[:task:<taskId>][:role:<roleName>]` — e.g. `agent:cody:project:wrkq`
- **Short form (`scopeHandle`)**: `<agentId>[@<projectId>[:<taskId>][/<roleName>]]` — e.g. `cody@wrkq`

For v1, handoffs accept either form and normalize down to the `project` kind. Task and role variants on the input parse, but the stored `scope_ref` is reduced to `agent:<agentId>:project:<projectId>`.

### Default scope resolution (env-first)

Handoff commands are expected to be called by an agent process that already has its runtime context. Resolution order:

1. `--scope <ref-or-handle>` flag (explicit override)
2. `ASP_SCOPE_REF` env var (parse, normalize to `project` kind)
3. `ASP_HANDLE` env var (parse, normalize to `project` kind)
4. `ASP_AGENT_ID + ASP_PROJECT` env vars (construct and validate)
5. Fail with a corrective error and an example

There is no `WRKQ_PROJECT_ROOT` fallback for scope — `WRKQ_PROJECT_ROOT` is a wrkq task-tree helper, not a platform project identity. Use `wrkq agent-context` to inspect what the CLI resolves at the current moment.

### Lifecycle

Two terminal states, no automation:

```
(created) ──→ pending ──→ acknowledge ──→ acknowledged
```

- **Pending** handoffs are eligible to be loaded into future agent context.
- **Acknowledged** handoffs are retained for history and search but do not appear in default pending listings.
- There is **no auto-expiration**, no auto-pruning, and no soft-delete in v1. Retirement happens only via `wrkq handoff acknowledge`.
- There is **no `superseded_by` relationship** in v1; an acknowledgement note is the only way to record why a handoff is obsolete.

### Idempotency

`wrkq handoff create --idempotency-key <key>` is keyed by `(scope_ref, idempotency_key)`:

- Same scope + same key + **same payload** → replays the existing handoff (no new row, idempotent).
- Same scope + same key + **different payload** → error with exit code 3 (`idempotency_payload_mismatch`).
- Different scope or different key → independent handoff.

This makes it safe for agents to retry a `handoff create` call without producing duplicates.

### Data Model Fields

A handoff record minimally carries:

| Field | Notes |
|-------|-------|
| `uuid`, `id` (`H-NNNNN`) | Stable identifiers |
| `scope_ref`, `scope_kind` | Canonical scope reference; `scope_kind = 'project'` for v1 |
| `agent_id`, `project_id` | Extracted from `scope_ref` for indexing |
| `agent_actor_uuid`, `project_container_uuid` | Optional FKs into `actors` and `containers` when a local row exists |
| `created_by_agent_id`, `created_by_actor_uuid` | Captures the acting agent at creation time |
| `title`, `body` | Markdown body; title and body are both required and non-empty |
| `status` | `pending` or `acknowledged` |
| `idempotency_key` | Unique per `(scope_ref, idempotency_key)` when set |
| `acknowledged_at`, `acknowledged_by_agent_id`, `acknowledged_by_actor_uuid`, `acknowledgement_note` | Acknowledgement metadata; null while pending |
| `meta` | Optional JSON blob for additional metadata |
| `etag`, `created_at`, `updated_at` | Standard optimistic-concurrency and timestamp fields |

The implementation lives in migration `000015_handoff_schema.sql` and `internal/store/handoffs.go`; see [CLI-REFERENCE.md](CLI-REFERENCE.md#handoffs-agent-session-continuity) for the verb surface.

---

## Planning Concepts

Tools for organizing and prioritizing work.

### Section (Kanban Column)

> **Note**: Sections are defined in the schema and domain types, but CLI commands for section management are not yet implemented.

A workflow stage for visualizing work. Sections represent where work sits in the process:

- **Backlog** - Not yet prioritized
- **Ready** - Prioritized and ready to start
- **Active** - Currently being worked on
- **Review** - Awaiting review or approval
- **Done** - Completed

Sections can have WIP (Work In Progress) limits to prevent overload.

### Priority

Relative importance of a task (1-4, where 1 is highest). Priority helps actors decide what to work on next when multiple tasks are available.

### Label

A tag for categorizing tasks orthogonally to the hierarchy. Labels enable filtering across projects:
- "backend" / "frontend"
- "urgent"
- "needs-design"

---

## Task Dependencies

How tasks relate to each other beyond the hierarchy.

### Blocks

Task A blocks Task B means B cannot start until A is complete. This models sequential dependencies:
- "Design API schema" blocks "Implement API endpoints"
- "Set up CI" blocks "Add automated tests"

### Relates To

An informational link between related tasks. No dependency implied, just awareness:
- "Update docs" relates to "Add new feature"
- "Fix bug X" relates to "Fix bug Y" (similar root cause)

### Duplicates

Indicates one task is a duplicate of another. The duplicate should typically be closed in favor of the original.

---

## Attribution & Audit

Every change in wrkq is attributed and recorded.

### Attribution

All modifications record who made them. This enables:
- Understanding who changed what
- Filtering to see one actor's work
- Accountability without authentication

### Event

A record of something that happened. Events are append-only - they can never be modified or deleted. The event stream provides:
- Complete history of all changes
- Ability to understand how state evolved
- Audit trail for compliance

---

## Concurrency Model

How multiple actors work without conflicts.

### Optimistic Concurrency

When updating, actors specify what version they're changing. If someone else changed it first, the update fails rather than silently overwriting. The actor must then:
1. Re-read the current state
2. Merge their changes with the new state
3. Retry the update

This enables multiple humans and agents to work on the same database safely.

---

## Addressing

How to refer to things in wrkq.

### Path

A filesystem-like address: `project/feature/task-slug`

Paths are human-readable and reflect the organizational hierarchy.

### Friendly ID

A short, memorable identifier: `T-00123`, `P-00007`

Friendly IDs are stable - they never change even if a task moves.

### Slug

The URL-safe name segment: `user-authentication`, `fix-login-bug`

Slugs are lowercase, use hyphens, and are unique among siblings.

---

## Diagram (Mermaid)

```mermaid
flowchart TB
    subgraph Actors["ACTORS"]
        Human["👤 Human<br/><small>Creates projects, reviews work</small>"]
        Agent["🤖 Agent<br/><small>Executes tasks autonomously</small>"]
        System["⚙️ System<br/><small>Maintains infrastructure</small>"]
    end

    subgraph Organization["ORGANIZATIONAL STRUCTURE"]
        Project["📁 Project<br/><small>Top-level workstream</small>"]
        Feature["✨ Feature<br/><small>Deliverable capability</small>"]
        Area["🔄 Area<br/><small>Cross-cutting concern</small>"]
    end

    subgraph WorkItems["WORK ITEMS"]
        Task["📋 Task<br/><small>Discrete unit of work</small>"]
        Subtask["📎 Subtask<br/><small>Part of larger task</small>"]
        Spike["🔍 Spike<br/><small>Time-boxed investigation</small>"]
        Bug["🐛 Bug<br/><small>Defect to fix</small>"]
        Chore["🧹 Chore<br/><small>Maintenance work</small>"]
    end

    subgraph States["WORKFLOW STATES"]
        Open(["Open"])
        InProgress(["In Progress"])
        Completed(["Completed"])
        Blocked(["Blocked"])
        Cancelled(["Cancelled"])
    end

    subgraph Collaboration["COLLABORATION"]
        Comment["💬 Comment<br/><small>Immutable message</small>"]
        Attachment["📄 Attachment<br/><small>Supporting file</small>"]
    end

    subgraph Dependencies["DEPENDENCIES"]
        Blocks["⛔ Blocks<br/><small>Must complete first</small>"]
        RelatesTo["🔗 Relates To<br/><small>Informational link</small>"]
        Duplicates["📑 Duplicates<br/><small>Same work item</small>"]
    end

    subgraph Planning["PLANNING"]
        Section["📊 Section<br/><small>Kanban column</small>"]
        Priority["🎯 Priority<br/><small>1-4 importance</small>"]
        Label["🏷️ Label<br/><small>Cross-cutting tag</small>"]
    end

    Event[["📜 Event<br/><small>Immutable audit trail</small>"]]

    %% Actor relationships
    Human & Agent -->|collaborates on| Project

    %% Organization hierarchy
    Project -->|contains| Feature & Area
    Feature & Area -->|contains| Task
    Project -->|contains| Task

    %% Work item relationships
    Task -->|has| Subtask
    Task -.->|can be| Spike & Bug & Chore

    %% State flow
    Open --> InProgress --> Completed
    InProgress --> Blocked
    InProgress --> Cancelled

    %% Task has collaboration
    Task -->|has| Comment & Attachment

    %% Task dependencies
    Task -->|relates via| Blocks & RelatesTo & Duplicates

    %% Planning
    Project -->|organized by| Section
    Task -->|has| Priority & Label

    %% Everything creates events
    Task & Comment & Project -.->|creates| Event

    %% Styling
    classDef actor fill:#dbeafe,stroke:#3b82f6
    classDef org fill:#dcfce7,stroke:#22c55e
    classDef work fill:#ffedd5,stroke:#f97316
    classDef state fill:#f3e8ff,stroke:#a855f7
    classDef collab fill:#fef9c3,stroke:#eab308
    classDef dep fill:#f1f5f9,stroke:#64748b
    classDef plan fill:#ccfbf1,stroke:#14b8a6
    classDef event fill:#e5e7eb,stroke:#6b7280

    class Human,Agent,System actor
    class Project,Feature,Area org
    class Task,Subtask,Spike,Bug,Chore work
    class Open,InProgress,Completed,Blocked,Cancelled state
    class Comment,Attachment collab
    class Blocks,RelatesTo,Duplicates dep
    class Section,Priority,Label plan
    class Event event
```

---

## Summary: Concept Relationships

```
HUMANS and AGENTS (actors) collaborate on PROJECTS

PROJECTS contain:
  - FEATURES (deliverable capabilities)
  - AREAS (cross-cutting concerns)
  - TASKS (work items)

FEATURES and AREAS contain TASKS

TASKS can be:
  - Regular tasks
  - Subtasks (children of tasks, one level deep)
  - Spikes (investigations)
  - Bugs (defects)
  - Chores (maintenance)

TASKS have:
  - STATE (open → in_progress → completed/cancelled/blocked)
  - PRIORITY (1-4)
  - LABELS (tags)
  - COMMENTS (discussion thread)
  - ATTACHMENTS (files)

TASKS relate via:
  - BLOCKS (must complete first)
  - RELATES TO (informational)
  - DUPLICATES (same work)

SECTIONS organize tasks into kanban columns within projects (planned, not yet in CLI)

AGENTS leave HANDOFFS for their own later sessions:
  - Scoped to (agent, project) in v1
  - Created → pending → acknowledged (manual retirement only)
  - Sibling to tasks/comments/memory, not a task subtype

All changes create EVENTS (immutable audit trail)
```
