You’re very close already. wrkq basically has:

- `container` = project/subproject tree  
- `task` = leaf under a container  
- plus actors, comments, attachments, event log, etc.  [oai_citation:0‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Your WRKQ-USAGE doc is already “using” containers as project/feature directories and tasks as work items.  [oai_citation:1‡WRKQ-USAGE.md](sediment://file_000000008cd871f59a386476ed09e0f0) The missing pieces for what you described are:

- an explicit concept of “board sections” (todo/doing/done) on features, and  
- first‑class subtasks, with a clean separation between **planning** (features in sections) and **execution** (tasks/subtasks in states).

Below is a concrete model that:

- keeps the filesystem-ish path semantics you already like,  
- adds Sections and Subtasks without making the hierarchy gross, and  
- gives agents a clean, queryable work queue.

---

## 1. Entities and relationships (conceptual)

Think in two layers:

- **Planning layer**: Projects → Features → Sections (kanban columns)  
- **Execution layer**: Tasks → Subtasks, with richer states

Non-hierarchical bit: “Section” is just metadata on a feature; it never appears in the path.

### 1.1 Project and Feature

Reuse the existing `container` table, but introduce a `kind` and a few planning fields.

**Container**

- `uuid`
- `id` (`P-xxxxx`)
- `slug`
- `title`
- `parent_id` (null = top-level project)
- **`kind`**: `project | feature | area | misc` (new)
- **`section_id`**: FK → `section.uuid` (new, used when `kind=feature`)
- **`priority`**: small int, for ordering features within a section (new)
- **`sort_index`**: optional explicit order in a section (new)
- `etag`, `created_at`, `updated_at`, `archived_at`, `created_by_actor_id`, `updated_by_actor_id` (as today)  [oai_citation:2‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Semantics:

- A **Project** is a `container` with `parent_id=NULL`, `kind='project'`.
- A **Feature** is a `container` whose parent is a project (or another feature) and `kind='feature'`.
- Features are the primary planning unit you drag across Sections.

This leverages your current addressing:

- `wrkq/auth` = project `auth`  
- `wrkq/auth/session-management` = feature container `session-management`  
- Tasks under that container are the execution items for the feature.

### 1.2 Section (per‑project kanban lanes)

New table:

```text
section
  uuid
  id             -- friendly, e.g. S-00001
  project_id     -- FK to container.uuid, must point at kind='project'
  slug           -- e.g. 'todo', 'doing', 'done'
  title          -- display name
  order_index    -- integer for column order
  wip_limit      -- optional int
  meta           -- JSON, optional
  created_at, updated_at, archived_at
```

Semantics:

- Each **project** defines its own ordered list of sections. Default might be:
  - `inbox`, `todo`, `doing`, `blocked`, `done`.
- A **feature** has at most one `section_id`:
  - New features default to a project’s configured “default” section (probably `todo`).
  - Boards are “features grouped by section” for a given project.
- Sections are entirely **orthogonal to paths** and tasks. They’re just a planning overlay on features.

This matches your “Section is like todo/doing/done but not the same as task state, and not part of the hierarchy”.

### 1.3 Task and Subtask

You already have `task` as a leaf under a container.  [oai_citation:3‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070) Extend it instead of inventing a new table.

**Task (extended)**

Existing fields (shortened):

- `uuid`
- `id` (`T-xxxxx`)
- `slug`
- `title`
- `project_id` (really “container_id”: FK to `container`)
- `state`
- `priority`
- `start_at`, `due_at`
- `labels` (JSON)
- `body`
- `etag`, `created_at`, `updated_at`, `completed_at`, `created_by_actor_id`, `updated_by_actor_id`  [oai_citation:4‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Add:

- **`kind`**: `task | subtask | spike | bug | chore` (at least `task | subtask` initially)
- **`parent_task_id`**: FK → `task.uuid`, nullable
- Optional: **`assignee_actor_id`**: FK → `actor.uuid` (even if you mostly use labels, it is nice for filters)
- Optional: **`estimate`**: numeric, `estimate_unit` (e.g. story points, hours)

Semantics:

- A **Task** is a row with `parent_task_id=NULL`, `kind in ('task','spike','bug','chore')`.
- A **Subtask** is a row with `parent_task_id` set and `kind='subtask'`. You get “task + subtasks” with no extra tables.
- Subtasks **live in the same container** as their parent:
  - Path stays `project/feature/parent-slug` or `project/feature/subtask-slug`.  
  - Hierarchy is logical (`parent_task_id`), not encoded in the path → exactly the “not fully hierarchical” behavior you said you want.

Depth:

- You can either:
  - allow arbitrary chains (task → subtask → sub‑subtask…), or  
  - enforce depth 1 at the application layer (recommended for sanity: subtasks cannot themselves have `parent_task_id` set).

For the agent / queue, you typically treat subtasks as first‑class work items; the parent task is more like a checklist header.

### 1.4 Task state vs Feature section

Make them **two independent axes**:

- Task `state` (execution)  
- Feature `section` (planning)

I’d align `task.state` with what WRKQ-USAGE already assumes:

```text
state ∈ { open, in_progress, blocked, completed, cancelled }
```

(and keep archival as a separate `archived_at` column, not a state value).  [oai_citation:5‡WRKQ-USAGE.md](sediment://file_000000008cd871f59a386476ed09e0f0)  

Some useful semantics:

- A feature in Section `todo` can have tasks in `open` or `blocked` states.  
- When **any** task under a feature moves to `in_progress`, you may auto‑bump the feature’s section from `todo` → `doing` (optional heuristic).  
- When **all** non‑cancelled tasks are `completed`, you may auto‑bump the feature’s section from `doing` → `done`.  
- Nothing stops a human from manually dragging a feature to another section; the automation should at most suggest changes.

### 1.5 Dependency edges (optional but very agent‑friendly)

If you want the agent to understand blocking, add a small relation table:

```text
task_relation
  from_task_id   -- FK task.uuid
  to_task_id     -- FK task.uuid
  kind           -- 'blocks' | 'blocked_by' | 'relates_to' | 'duplicates'
  meta           -- JSON
  created_at, created_by_actor_id
```

For the agent’s queue you then filter “ready” items as:

- `state = open`
- no incoming `blocked_by` edge from tasks that are not yet `completed` or `cancelled`
- feature’s section is not `done`.

---

## 2. Mapping this back onto wrkq’s existing model

This section is about **incremental changes**, not a rewrite.

### 2.1 Containers → Projects and Features

Minimal migration:

- Add column `container.kind` (default `project` for top‑level, `area` for existing subprojects).
- Annotate any containers you treat as “features” with `kind='feature'`. Your USAGE doc already uses “subdirectories for major features”; you can just formalize: “container deeper than depth 1 under a project may be a `feature`”.  [oai_citation:6‡WRKQ-USAGE.md](sediment://file_000000008cd871f59a386476ed09e0f0)  
- Add `container.section_id` (nullable).
- Optionally add `container.priority` and `container.sort_index`.

No existing CLI behavior has to change immediately; you can phase in new semantics behind flags (e.g. `wrkq ls --features-board`).

### 2.2 Sections

Add `section` table as above, plus very small API/CLI surface later:

- `wrkq section ls <project>`
- `wrkq section add <project> <slug> -t "Doing" --order 20 --wip 3`
- `wrkq section set <feature-path> <section-slug>`

Internally:

- `wrkq section add` inserts into `section`.
- `wrkq section set` updates `container.section_id` for a `kind='feature'` container.

Note: this keeps the “no sections in addressing model” invariant from the spec; paths are unchanged.  [oai_citation:7‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

### 2.3 Tasks & Subtasks

Schema changes:

- Add `task.kind` (`task` default).
- Add `task.parent_task_id` (nullable).
- Optionally add `task.assignee_actor_id`, `task.estimate`, `task.estimate_unit`.

State:

- Update the enum behind `task.state` to match `open | in_progress | blocked | completed | cancelled`. This lines up with your WRKQ-USAGE lifecycle docs.  [oai_citation:8‡WRKQ-USAGE.md](sediment://file_000000008cd871f59a386476ed09e0f0)  
- Keep `archived_at` as the “soft delete / hide from default views” mechanism, as already in the spec.  [oai_citation:9‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Behavior:

- Parent task progress is summary only; subtasks are where the agent mostly works.
- You can have commands like:
  - `wrkq subtask add <parent-task> <slug> -t "..."` → creates a `task` row with `parent_task_id`.
  - `wrkq ls <container> --group-by=parent_task` → present parent+subtasks hierarchically.

Crucially, nothing about the **path format** changes. A subtask is just another task in the same container.

---

## 3. How this supports “lite planning” + “agent work queue”

### 3.1 Lite planning

At the planning level you mostly interact with:

- Projects (`containers` with `kind='project'`)
- Features (`kind='feature'`)
- Sections (per-project board lanes)

You can build:

- a **board view**: features grouped by section, ordered by `section.order_index`, then `container.priority` or `sort_index`;  
- a **roadmap view**: features with counts of open/in-progress/completed tasks/subtasks.

Simple aggregate queries:

- “Feature completion %” = completed leaf tasks / total leaf tasks under feature.
- “Show features blocked” = features that have at least one task with state `blocked`.

### 3.2 Agent work queue

For a coding agent you define a view something like:

> All tasks or subtasks where  
> - `state in (open, in_progress, blocked)`  
> - parent feature’s section is in (`todo`, `doing`)  
> - (optional) `assignee_actor_id` is the agent, or a label `agent:<name>` is present  
> - (optional) no unresolved `blocked_by` relations.

That gives the agent a crisp queue of “things it’s allowed to touch”, while humans keep higher‑level planning by moving **features** between Sections.

Because this all sits inside your existing event log / etag / comments machinery, you keep auditability and nice collaboration semantics.  [oai_citation:10‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

---

## 4. Design choices / knobs worth deciding

These aren’t blockers, but you’ll get a cleaner v1 if you pick a stance:

1. **Feature vs Container:**  
   I’ve assumed “feature = container with `kind='feature'`”. That keeps addressing and the current tree model intact. If you’d rather have a separate `feature` table, the main tradeoff is: clearer semantics vs more joins and more divergence from the filesystem mental model.

2. **Subtask depth:**  
   Do you want to hard‑cap at one level of subtasks, or allow arbitrary nesting? I’d strongly lean to “one level only” and enforce that in the CLI / service.

3. **Auto‑movement of features between sections:**  
   You can either:
   - store `section_id` and mutate it based on task events (plus manual overrides), or  
   - treat Section as a purely manual planning tool and compute “suggested section” in the UI.  
   Storing + lightly auto‑updating seems like the sweet spot.

4. **Assignment model:**  
   If the primary consumer is “a coding agent”, you might skip a formal `assignee` field and keep using tags (`labels` JSON) initially. But if you want human + multiple agents later, a proper `assignee_actor_id` is low-cost and pays off in filtering.

5. **Dependencies now vs later:**  
   The `task_relation` table is optional. If you don’t add it in v1, you’ll probably emulate “blocked by X” with labels and comments for a while; that’s okay, just more parsing work for agents.

---

If you’d like, next iteration we can sketch:

- specific migrations from the current schema, and  
- how `ls`, `find`, and maybe a `board` command would surface Sections, features, and subtasks without breaking existing scripts.
---
Answers to (4):
1.  Feature is a container 
2.  One level of subtask max
3.  Store + lightly auto-update
4.  Primary consumer of tasks/implementation is an agent.  Primary consumer of planning is an end-user.  Add a proper assignee actor id
5.  task_relation now

Regarding agent work queue (3.2):
This implies static sections (kanban style). I’m good with todo/doing but I prefer some flexibility for the other section names/number of sections.  

Other thoughts:
Since plan mode is mostly for end-users accessing via web UI, perhaps we don’t surface sections in CLI?  Need thoughts here.
---
Cool, this is enough to lock in a concrete shape.

I’ll fold your decisions into the model, then answer the two big things you just raised:

- how to do **flexible sections** without breaking the agent queue, and  
- how much to **surface sections in the CLI** vs “plan mode” web UI.

All of this stays compatible with the existing spec: container + task as core tables; no sections in the addressing model; single SQLite DB.  [oai_citation:0‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  [oai_citation:1‡WRKQ-USAGE.md](sediment://file_000000008cd871f59a386476ed09e0f0)  

---

## 1. Updated core model with your decisions baked in

### 1.1 Containers → projects, features, sections

Keep `container` as-is, but refine semantics:

- `container.kind`: `project | feature | area | misc` (new column).
  - `project`: top-level planning unit.
  - `feature`: workstream within a project; canonical planning unit.
- `container.section_id`: nullable FK → `section.uuid` (used only when `kind='feature'`).
- `container.priority` / `sort_index`: optional small ints for ordering features within a section.

Everything else about containers stays as spec’d.  [oai_citation:2‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

New `section` table, per project:

```text
section
  uuid
  id             -- friendly, e.g. S-00001
  project_id     -- FK container.uuid, kind='project'
  slug           -- 'todo-this-week', 'doing', 'qa', 'done', etc.
  title          -- display label
  order_index    -- int, left-to-right order
  role           -- enum: 'backlog' | 'ready' | 'active' | 'review' | 'done'
  is_default     -- bool (new features start here)
  meta           -- JSON, reserved for UI/layout knobs
  created_at, updated_at, archived_at
```

Key points:

- Section names and counts are fully flexible; you can have arbitrary slugs and titles per project.
- `role` gives the agent stable semantics even if names differ:
  - `ready` ≈ “todo”.
  - `active` ≈ “doing”.
  - `backlog`, `review`, `done` for everything else.
- Multiple sections may share the same role (e.g. `this-week` and `next-week` both `ready`).

A **feature** is a `container` with `kind='feature'` whose `parent_id` points at a project (or another feature if you want nesting). Its “column” is `section_id`, interpreted with `section.role`.

### 1.2 Tasks, subtasks, assignee

Extend the existing `task` table rather than inventing new resources:

- Add `task.kind`: at minimum `task | subtask | spike | bug | chore` (you can start with `task | subtask`).
- Add `task.parent_task_id`: nullable FK → `task.uuid`.
- Add `task.assignee_actor_id`: nullable FK → `actor.uuid`.

Everything else in `task` stays the same: slug, title, project_id (FK to container), priority, labels JSON, timestamps, etag.  [oai_citation:3‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Semantics with your constraints:

- A top‑level task has `kind!='subtask`, `parent_task_id IS NULL`.
- A subtask has `kind='subtask'`, `parent_task_id` set.
- Enforce “one level of subtask” in domain logic: any insert/update that sets `parent_task_id` must point at a task whose `parent_task_id IS NULL`. No recursive nesting.

Assignee semantics:

- `assignee_actor_id = NULL` means unassigned.
- For an agent, the default queue filter is “tasks where `assignee_actor_id = this agent` OR `assignee_actor_id IS NULL` but the task is explicitly marked agent-eligible via labels”.

You can encode “agent-eligible” as labels like `agentable` or `agent:<slug>`; that avoids trying to overload `assignee` for both “owner” and “allowed worker”.

### 1.3 Task lifecycle vs feature section

You already use a richer lifecycle in practice:

- `open`, `in_progress`, `completed`, `blocked`, `cancelled`.  [oai_citation:4‡WRKQ-USAGE.md](sediment://file_000000008cd871f59a386476ed09e0f0)  

I’d standardize `task.state` to that enum and stop overloading `archived` as a state; `archived_at` remains the separate soft-delete flag from the PRD.  [oai_citation:5‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Feature sections are independent:

- Feature `F` has a `section_id` → `section.role`.
- Tasks and subtasks under `F` have their own `state`.

Light auto-update policy (your “store + lightly auto‑update”):

1. If `F` is in a section whose role is `backlog` or `ready`, and any task/subtask under `F` transitions to `in_progress`, move `F` to the first `section` in that project with `role='active'` (ordered by `order_index`).

2. If `F` is in a section whose role is not `done`, and all non-cancelled tasks/subtasks under `F` are `completed`, move `F` to the first `section` with `role='done'`.

3. Manual moves by the user (web UI or CLI) always win; auto‑moves only fire on future task state changes.

You can refine that later (e.g., consider `review` role), but this gets you the “todo/doing/done” feeling while letting you name columns however you want.

### 1.4 Dependencies: `task_relation` now

For the agent queue, you mostly need “blocks / blocked by”. I’d keep this tight:

```text
task_relation
  from_task_id   -- FK task.uuid (the blocker)
  to_task_id     -- FK task.uuid (the blocked task)
  kind           -- 'blocks' | 'relates_to' | 'duplicates'
  meta           -- JSON (optional)
  created_at
  created_by_actor_id
  PRIMARY KEY (from_task_id, to_task_id, kind)
```

Conventions:

- Only store `kind='blocks'` edges once (blocker → blocked). “blocked by” is just reading that in reverse.
- Agent’s “ready” tasks are ones with:
  - no incoming `kind='blocks'` from tasks whose state is not `completed` / `cancelled`.

This is enough to express “don’t start implementation until the design task is done” without touching sections.

---

## 2. Flexible sections + agent queue

You want kanban-ish behavior but don’t want to be locked into static `todo/doing/done` columns.

With the `role` field above you get:

- Arbitrary number and naming of sections per project.
- Stable semantics for the agent.

Example per-project configuration:

```text
slug         title          role      order_index
--------------------------------------------------
icebox       Icebox         backlog   10
backlog      Backlog        backlog   20
ready        Ready          ready     30
this-week    This Week      ready     40
doing        Doing          active    50
blocked      Blocked        active    60
review       In Review      review    70
done         Done           done      80
```

This satisfies:

- Human planning: a detailed board with “Icebox”, “This Week”, “Blocked”, etc.
- Agent logic: just treat `role in ('ready','active')` as “features I can pull tasks from”.

Agent queue definition in this model (conceptually):

1. Only consider `task`/`subtask` rows where:
   - `state IN ('open','in_progress','blocked')`
   - `archived_at IS NULL`.

2. Join through container → feature → section:
   - Task’s `project_id` (container) must be a feature or live under a feature.
   - That feature’s `section.role IN ('ready','active')`.

3. Apply dependency and assignee filters:
   - No unresolved incoming `blocks` edges.
   - `assignee_actor_id IS NULL` or equal to the current agent, and/or labels mark it as agentable.

The feature’s section roles give you the “this is actually active work vs backlog vs done” axis; task state and relations give you the execution axis.

---

## 3. Should sections show up in the CLI?

You’re right that the **primary consumer of planning is the web UI**, and the CLI should stay path‑centric and task‑centric. But agents still need some way to see feature sections to build their queue.

I’d split it into **three exposure levels** and recommend starting at level 2:

1. **Level 1 – DB‑only**: sections exist purely for the web UI; CLI ignores them.  
   This breaks the “agent work queue” requirement unless agents talk directly to an HTTP API.

2. **Level 2 – Read + simple filter in CLI** (recommended starting point):  
   CLI surfaces section metadata and filters, but doesn’t try to be a kanban tool.

3. **Level 3 – Full board commands in CLI**: `wrkq board`, explicit `wrkq section ...` subcommands etc. Probably overkill for now.

### 3.1 Minimal CLI surfacing that keeps the CLI feeling “Unix-y”

I’d do this:

- Add section fields to container outputs:
  - `section_slug`, `section_title`, `section_role` in `wrkq ls`, `wrkq stat`, `wrkq find` for containers.  [oai_citation:6‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  
- Add a `--section-role` and/or `--section` filter on `wrkq find` for containers:
  - `wrkq find myproj --type p --section-role ready,active --json` → all features in ready/active roles.
- Optionally add a simple mutator for features:
  - Either `wrkq set myproj/feature --section ready` (with `-type p`), or
  - `wrkq set myproj/feature section_slug=this-week`.

No separate `wrkq section` subcommand yet, no board view in the terminal. The mental model for CLI users stays: “containers + tasks”, just with a couple extra columns.

Agents then get what they need entirely from CLI:

- Discover ready features:
  - `wrkq find myproj --type p --section-role ready,active --json`.
- Pull tasks under those features:
  - `wrkq ls 'myproj/feature/**' -type t --json` and filter by task state, labels, assignee, and relations.

### 3.2 Web UI as the primary section editor

On the web UI side you can go richer:

- Create and reorder sections per project visually.
- Configure `role` per section and mark which one is default.
- Drag features between sections.
- Show feature progress as aggregates of underlying tasks/subtasks.

All of that hits the same `section` + `container.section_id` + `role` schema, leaving the CLI relatively simple.

If later you feel the need for CLI editing, you can add a small, non-fancy surface:

- `wrkq section ls myproj` – lists sections (good for debugging configs).
- `wrkq section set myproj/feature this-week` – single operation to move a feature.

But you don’t need to expose that in v1.

---

## 4. Edge cases and failure modes worth being explicit about

A few constraints that keep this predictable for both agent and human:

- **Feature-less tasks**:  
  Tasks in containers that aren’t `kind='feature'` (e.g., `inbox`) can either:
  - be treated as a special “unscoped” work queue (agent only pulls if explicitly told), or
  - be auto-associated with a synthetic feature.  
  I’d keep `inbox` as a freeform space that the agent ignores unless tasks are assigned directly to it via `assignee_actor_id`.

- **Features with no tasks**:  
  A feature without tasks can move between sections freely in the web UI; auto-move rules simply never trigger because they depend on task state transitions.

- **Multiple “active” or “ready” sections**:  
  That’s allowed by design. For the agent, the decision is whether to:
  - treat all `role='ready'` equally, or
  - check `order_index` to pull from the left-most/highest-priority ready section first.  
  You can decide that in the queue implementation without changing the schema.

- **Subtasks vs aggregate state**:  
  You need to choose how to treat parents for auto-move logic. Easiest is:
  - consider both tasks and subtasks when computing “any in_progress” / “all done” for a feature, and
  - treat a parent task’s own `state` as cosmetic if it has subtasks (e.g., set it to `in_progress` whenever any of its subtasks is in progress, `completed` when all subtasks are done).  
  That aggregation can be implemented as a view or cheap query; no schema changes needed.

---

If you’d like, next pass we can:

- sketch concrete migration SQL from the current schema to: `container.kind`, `section`, `task.kind`, `parent_task_id`, `assignee_actor_id`, `task_relation`, and  
- design the exact `wrkq find`/`ls` invocations your agent would use to build its queue and pick the “next best task” given sections, dependencies, and assignee.
---
Review the workflow at https://docs.conductor.build/workflow

I like this concept of a workspace per feature.  Let’s expand that and incorporate into our wrkq data model/spec. 

Give me a high-level data model when you’re done.  

---
Conductor’s docs describe a workspace as an **isolated copy + branch of your repo**, created per feature/bugfix, where you:  
create workspace → develop → review/test → create PR + merge → archive workspace (restorable later with history).  [oai_citation:0‡Conductor](https://docs.conductor.build/workflow)  

Below is a high‑level wrkq data model that bakes in that “workspace per feature” pattern on top of your existing containers/tasks, sections, and agent workflow.  [oai_citation:1‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  [oai_citation:2‡WRKQ-USAGE.md](sediment://file_000000008cd871f59a386476ed09e0f0)  

---

## 1. Core structural entities

### Actor  
Unchanged from spec.

- Represents human, agent, or system.  
- Used for attribution on all writes and workspace membership/ownership.

Key fields (conceptual):  
`id`, `uuid`, `slug`, `role (human|agent|system)`, `display_name`, `meta`, timestamps.

---

### Container → Project and Feature

You already have a `container` table; we refine semantics:

- `Container.kind` ∈ `project | feature | area | misc`.
- `Project` = `container` with `parent_id = NULL`, `kind='project'`.
- `Feature` = `container` with `kind='feature'` whose parent is a project (or feature, if you allow nesting).

Extra fields you’ve already been trending toward:

- `section_id` → FK to `Section` (planning lane for this feature, see below).
- `priority`, `sort_index` for ordering within a section.
- Standard timestamps + `archived_at` and actor FKs as in spec.  [oai_citation:3‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

This gives you “feature as primary planning unit” and the anchor for “workspace per feature”.

---

### Section (per‑project kanban columns with roles)

New table, per project, to give planning lanes without affecting paths:

- `id`, `uuid`
- `project_id` → `Container` (must be `kind='project'`)
- `slug` (e.g. `backlog`, `ready`, `this-week`, `doing`, `review`, `done`)
- `title`
- `order_index` (left‑to‑right)
- `role` ∈ `backlog | ready | active | review | done`
- `is_default` (bool; where new features go)
- `meta` (JSON)
- `archived_at`, timestamps

Features point at a section via `container.section_id`. The **role** gives the agent stable semantics even if names/column counts vary per project.

---

## 2. Workspace per feature

### Workspace

This is the Conductor‑style unit of work per feature:

> “For each feature or bugfix, create a new workspace… Each workspace is an isolated copy and branch of your Git repo… then develop, review/test, create PR, and archive.”  [oai_citation:4‡Conductor](https://docs.conductor.build/workflow)  

Model it as a first‑class entity in the canonical wrkq DB:

- `uuid`
- `id` (friendly, e.g. `W-00001`)
- `slug` (unique within a feature or globally)
- `title`

Relations:

- `feature_id` → `Container` (`kind='feature'`).
- `project_id` → `Container` (redundant but convenient for queries).
- `created_by_actor_id`, `updated_by_actor_id` → `Actor`.
- Optional `log_task_id` → `Task` (see below; a task that acts as the workspace’s “chat/log” anchor reusing the existing comment model).

Git / code context:

- `repo_url` or `repo_slug`
- `branch_name` (workspace branch)
- `base_commit_sha` (where workspace branched from main)
- `head_commit_sha` (last known head)

Execution / environment context (kept loose):

- `kind` ∈ `human | agent | mixed` (dominant driver of the workspace).
- `meta` JSON for environment details:
  - `db_snapshot_path` (from `wrkqadm db snapshot`, if you want to track it)  
  - `attach_dir` override for this workspace  
  - external IDs (GitHub PR, Linear issue, etc.)

Lifecycle:

- `state` ∈ `active | archived | deleted`
- `created_at`, `updated_at`, `archived_at`

This gives you:

- Recommended pattern: **1 active workspace per feature**, but the schema supports multiple (e.g., spike vs full implementation).
- Clear anchor for “this set of code + wrkq changes + conversations belongs to this feature”.

---

### Workspace ↔ wrkq DB + git

You already have:

- `wrkqadm db snapshot` — point‑in‑time SQLite copy for agents/CI.  [oai_citation:5‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  
- `wrkq bundle create` / `wrkqadm bundle apply` — patch/bundle flow for PRs.  [oai_citation:6‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Tie these to `Workspace` conceptually:

- When a workspace is created for feature `F`:
  - Git: create branch `feature/<slug>` from main.
  - DB: `wrkqadm db snapshot` → ephemeral DB; path stored in `Workspace.meta.db_snapshot_path` (optional but useful).
- Agent and human work inside that workspace’s branch + DB snapshot.
- When ready for PR:
  - `wrkq bundle create` from the workspace DB for wrkq changes.
  - Normal git diff/PR for code.
  - Link the resulting PR URL and bundle location into `Workspace.meta` (e.g. `meta.pr_url`, `meta.bundle_path`).
- After merge:
  - `wrkqadm bundle apply` into canonical DB.
  - Mark `Workspace.state = archived`, set `archived_at`.

The canonical DB acts as the “index of workspaces”, but the heavyweight data (workspace DB copies, git worktrees) live in the filesystem.

---

### Workspace history / chat

Conductor archives workspace chat and lets you restore the workspace with its history later.  [oai_citation:7‡Conductor](https://docs.conductor.build/workflow)  

You already have comments as a first‑class chat/collab mechanism on tasks.  [oai_citation:8‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Instead of inventing a new comment system, link each workspace to a “log task”:

- On workspace creation, optionally create a `Task` under the feature container, e.g. slug `workspace-log` or `workspace-w-00001`.
- Store its `task_id` as `Workspace.log_task_id`.
- All high‑level workspace‑level comments/conversation go on that task via normal `wrkq comment` commands.

Because comments already emit to the event log with actor attribution, you automatically get “restorable chat history per workspace” by:

- filtering comments by `task_id = Workspace.log_task_id`, and
- filtering events by `workspace_id` (see next section) when showing structured history.

---

## 3. Tasks, subtasks, dependencies

### Task / Subtask

Extend your existing `task` table minimally.  [oai_citation:9‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Core fields:

- `uuid`, `id` (`T-xxxxx`), `slug`, `title`
- `project_id` → `Container` (still the immediate container, whether project or feature)
- `kind` ∈ `task | subtask | spike | bug | chore`
- `parent_task_id` → `Task` (nullable; if set, this is a subtask)
- `state` ∈ `open | in_progress | blocked | completed | cancelled` (as in your usage doc).  [oai_citation:10‡WRKQ-USAGE.md](sediment://file_000000008cd871f59a386476ed09e0f0)  
- `priority` 1–4
- `labels` JSON
- `start_at`, `due_at`
- `assignee_actor_id` → `Actor` (for agent vs human assignment)
- `body` (markdown)
- timestamps + `archived_at`, `completed_at`, `etag`

Constraint:

- If `parent_task_id IS NOT NULL`, the parent’s `parent_task_id` must be `NULL` → **one level of subtasks max**.

Tasks don’t belong *to* a workspace; they belong to a feature (container). A workspace “uses” a subset of tasks (and maybe creates new ones), which you can recover via the event log or a linking mechanism.

---

### TaskRelation (dependencies)

A thin dependency graph:

- `from_task_id` → blocker task
- `to_task_id` → blocked task
- `kind` ∈ `blocks | relates_to | duplicates`
- `meta` JSON
- `created_at`, `created_by_actor_id`

Agent’s “ready queue” logic:

- `state ∈ ('open','in_progress','blocked')`
- task’s feature’s `Section.role ∈ ('ready','active')`
- no incoming `blocks` from non‑`completed`/`cancelled` tasks
- optional filters on `assignee_actor_id` and labels (agent eligibility).

Workspaces then just **scope** which features are in play.

---

## 4. Events and workspace association

You already have a canonical event log with `resource_type`, `resource_id`, `actor_id`, `timestamp`, `payload`.  [oai_citation:11‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  

Extend it slightly:

- Add nullable `workspace_id` → `Workspace`.
- For any operation executed while `WRKQ_WORKSPACE_ID` (or similar) is set in env/config, the CLI writes events with that `workspace_id`.

That gives you:

- A full “workspace history” view: filter `events` by `workspace_id`.
- Clean mapping of “which tasks/subtasks/comments were created or modified in this workspace”.
- Ability to generate a `wrkq bundle create` that’s implicitly “all events in workspace W since creation” (without relying only on timestamps).

---

## 5. Very high‑level ER sketch (conceptual)

Cardinalities (conceptual, not enforced strictly where you want flexibility):

- `Project (Container.kind='project')`
  - 1‑N `Feature` (Container.kind='feature')
  - 1‑N `Section`
- `Feature`
  - 0‑N `Workspace`
  - 0‑N `Task` (and subtasks via `Task.parent_task_id`)
- `Workspace`
  - 1 `Feature`
  - 0‑1 `log_task` (`Task`)
  - 0‑N `Event` (via `workspace_id`)
- `Task`
  - 0‑N `Subtask` (Tasks with `parent_task_id`)
  - 0‑N `Comment`
  - 0‑N `TaskRelation` edges in/out
- `Actor`
  - 0‑N `Task` (via `assignee_actor_id`)
  - 0‑N `Workspace` (creator/owner)
  - 0‑N `Event`, `Comment`, etc.

This keeps:

- the **filesystem‑shaped** feel of wrkq: containers + tasks, single SQLite DB, path‑based addressing;  [oai_citation:12‡SPEC.md](sediment://file_00000000eea0720cbd4486ab402d1070)  
- the planning layer: `Feature` + `Section` roles for flexible kanban;
- the execution layer: `Task`/`Subtask` + `TaskRelation` + `state`;
- and overlays a Conductor‑style **workspace per feature** flow by introducing a first‑class `Workspace` entity that ties branch, DB snapshot, PR/bundle, and history together without disturbing paths or task semantics.
