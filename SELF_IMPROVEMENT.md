# Hermes Self-Improvement Architecture Assessment

Source baseline: `~/tools/hermes-agent` at commit `67011cc0d`.

This document studies how Hermes implements self-improving agents and extracts
the parts most relevant for building a self-improvement system in praesidium.
Hermes does not improve by training model weights. It improves by evolving the
scaffold around the model: memory, skills, session recall, review workers,
curation, automation, and harness rules.

The core lesson is that self-improvement should be treated as a product surface,
not a side effect. Hermes gives the agent explicit mechanisms for recording
declarative knowledge, turning experience into reusable procedures, recalling
prior episodes, and pruning its own procedural memory over time.

## Executive Summary

Hermes has a credible scaffold-level self-improvement loop:

1. The foreground agent works on a user request.
2. The harness tracks whether enough activity has occurred to justify review.
3. After the visible response is delivered, a background reviewer may run.
4. The reviewer is restricted to memory and skill tools.
5. It can save durable facts, update user profile notes, create skills, or patch
   existing skills.
6. Future sessions rebuild their prompt from these artifacts.
7. A curator later consolidates, stales, archives, and reports on agent-created
   skills.

Hermes separates improvement into three durable memory classes:

- Declarative memory: facts about the user, environment, agent preferences, and
  stable cross-session knowledge.
- Procedural memory: skills, support files, scripts, templates, and operating
  procedures.
- Episodic memory: prior sessions and message history, searchable through
  SQLite FTS.

That separation is the most important design choice. It prevents task history
from becoming bloated user memory, prevents procedural knowledge from being
buried in chat transcripts, and gives the curator a concrete object model to
maintain.

For praesidium, the most useful features to copy are:

- A post-turn or post-task review worker that can only write through typed
  memory and skill APIs.
- A skill system with metadata, provenance, usage tracking, patch history,
  support files, validation, and progressive disclosure.
- A curator that archives and consolidates only agent-created skills, with
  pinning, reports, and rollback.
- A session search layer that agents are expected to use before claiming they
  do or do not remember prior work.
- Strict separation between foreground task execution and background
  self-modification.
- Stable prompt construction that avoids mutating instructions mid-session.
- Event hooks that let subagents, cron jobs, webhooks, and workers contribute
  durable observations without polluting memory directly.

The biggest risks are noisy skill creation, over-aggressive curation, stale or
poisoned memory, silent degradation of external memory providers, and
self-modifying behavior without enough auditability.

## Source Map

Important Hermes files:

- `run_agent.py`: thin compatibility wrapper around the new agent package.
- `agent/agent_init.py`: constructs the agent, config, memory stores, context
  engine, and tool schemas.
- `agent/conversation_loop.py`: main tool loop, memory prefetch/sync,
  self-improvement nudges, and background review spawning.
- `agent/system_prompt.py`: stable/context/volatile system prompt construction.
- `agent/background_review.py`: post-turn self-improvement reviewer.
- `agent/memory_provider.py`: external memory provider interface.
- `agent/memory_manager.py`: built-in and external memory orchestration.
- `tools/memory_tool.py`: built-in file-backed memory tool.
- `tools/skills_tool.py`: skill discovery and reading.
- `tools/skill_manager_tool.py`: skill creation, patching, editing, and
  deletion.
- `tools/skill_usage.py`: usage/provenance telemetry and archival state.
- `agent/curator.py`: background skill curator.
- `tools/session_search_tool.py`: episodic session recall.
- `tools/delegate_tool.py`: subagent orchestration.
- `tools/code_execution_tool.py`: programmatic tool calling.
- `cron/scheduler.py`: scheduled routine execution.
- `gateway/platforms/webhook.py`: webhook-triggered agent runs.
- `agent/context_compressor.py`: context compression and long-session
  continuity.
- `plugins/memory/honcho/`: external user-modeling provider.

## Mental Model

Hermes self-improvement is best understood as five loops running at different
timescales.

### 1. Foreground Tool Loop

The foreground loop answers the user. It can directly use memory, session
search, skills, delegation, cron, and code execution tools. It is the only loop
that should optimize for immediate task completion.

Useful property: the foreground loop is not forced to self-modify. It can, but
most durable writes are deferred to review.

### 2. Memory Sync Loop

At the start of a turn, Hermes can prefetch relevant external memory and inject
it into the model call. At the end of the turn, it can sync the user message and
assistant response to an external provider.

Useful property: prefetched memory is injected into the API-call copy of the
user message, not into persisted conversation history. This limits context
pollution and prevents memory blocks from becoming part of the transcript.

### 3. Background Reflection Loop

After the visible response, Hermes may fork a reviewer. The reviewer inspects
the transcript and decides whether to write memory or skill updates.

Useful property: reflection does not compete with the foreground response. It
can run quietly and report only a compact status.

### 4. Skill Curation Loop

Over days or weeks, the curator reviews agent-created skills. It can mark them
stale, archive unused ones, consolidate overlapping skills, and generate
reports.

Useful property: autonomous learning is balanced by autonomous garbage
collection.

### 5. Automation Loop

Cron jobs, webhooks, kanban workers, and subagents can perform work outside a
normal interactive turn. Hermes treats these as harness contexts with different
memory rules, delivery modes, and toolsets.

Useful property: self-improvement is not limited to chat. Scheduled jobs and
workers can also exercise skills, generate observations, and produce searchable
history.

## Harness and Conversation Loop

The conversation loop is the spine of the system.

Key behaviors:

- It restores or builds the system prompt once per session.
- It keeps the prompt stable for prefix caching.
- It tracks user turns for memory-review nudges.
- It tracks tool iterations for skill-review nudges.
- It prefetches external memory before tool execution.
- It syncs memory after a completed turn.
- It spawns background review after the user-visible response.
- It avoids background review on interrupted turns.

The important architectural choice is that Hermes does not mutate the active
system prompt every time memory or skills change. Built-in memory and the skill
index are captured into the session prompt snapshot. Writes become visible in a
future session or after a prompt rebuild/compression path.

This is the right tradeoff for a production agent. Immediate mutation can create
non-reproducible behavior and damage prompt caching. Stable sessions are easier
to reason about, and durable writes still matter across future work.

### Nudge Counters

Hermes uses counters rather than reviewing every turn:

- Memory review defaults to every 10 user turns.
- Skill review defaults to every 10 tool-calling iterations.

This saves cost and reduces noise. It also means important one-off corrections
may not be captured unless the foreground agent proactively calls memory or
skill tools.

Recommendation for praesidium:

- Keep counters, but add event-based triggers.
- Trigger review immediately for user corrections, repeated tool failure,
  completed tasks, manually accepted work, and explicit "remember this" signals.
- Keep periodic review as a fallback.

### System Prompt Tiers

Hermes builds its system prompt from tiers:

- Stable: identity, tool-use guidance, memory guidance, skill index,
  environment hints.
- Context: caller-provided system message and local context files.
- Volatile: date, session id, memory snapshot, external memory block.

The whole prompt is cached in the session database. This supports prefix cache
reuse and ensures gateway-created fresh agent instances can restore exactly the
same prompt.

Recommendation for praesidium:

- Adopt tiered prompt construction.
- Store the exact prompt used for each session.
- Treat the stored prompt as audit data.
- Make prompt rebuilds explicit events.

## Declarative Memory

Hermes has built-in file memory plus optional external memory providers.

### Built-In File Memory

The built-in memory tool manages two files under the Hermes home directory:

- `MEMORY.md`: agent personal notes and durable operating facts.
- `USER.md`: durable profile facts and preferences about the user.

Important details:

- File writes use locking and atomic replacement.
- Entries are parsed into discrete units.
- Character budgets keep prompt snapshots bounded.
- Writes are scanned for prompt-injection patterns.
- Loading also scans entries and blocks suspicious ones from prompt injection.
- Drift detection refuses writes if the on-disk file would not round-trip.
- Memory writes update the live file but not the current prompt snapshot.

This is simple, inspectable, and good enough for a local-first system.

The tool guidance is also conceptually important:

- User preferences and durable corrections belong in memory.
- Procedural knowledge belongs in skills.
- Task history belongs in session search.

That distinction should be copied.

### External Memory Providers

Hermes defines a provider interface with hooks for:

- initialize
- system prompt block
- prefetch
- queued prefetch
- sync turn
- tool schemas
- tool call handling
- turn start
- session end or switch
- pre-compression
- memory write mirroring
- delegation observation

The memory manager orchestrates built-in memory plus at most one external
provider. Provider errors are nonfatal, which protects the foreground agent.

This design is strong because external memory is a plugin, not a dependency of
the core loop.

Recommendation for praesidium:

- Define a memory provider interface early, even if the first implementation is
  only local SQLite.
- Use one active provider per agent context at first.
- Treat provider failure as degraded mode, but surface health clearly.
- Record which provider supplied which memory block.

### Memory Injection and Scrubbing

Hermes wraps prefetched memory in a distinct memory-context block and injects it
into the model call. It also includes a streaming scrubber to prevent memory
context from leaking into visible output.

This is a subtle but important feature. Retrieval context should inform the
model, but should not be echoed back to the user as if it were answer content.

Recommendation for praesidium:

- Treat retrieved memory as a separate channel conceptually, even if the model
  API only supports text.
- Sanitize nested memory blocks before saving or syncing.
- Scrub assistant output for accidental recall-context leakage.

### Honcho Provider

Honcho is the most relevant external provider in Hermes. It provides persistent
user modeling, session/peer identity, dialectic Q&A, and configurable context
injection.

Modes:

- `context`: automatically inject memory context.
- `tools`: expose memory tools but do not automatically inject.
- `hybrid`: use both.

Useful ideas:

- Separate runtime user identity from provider peer identity.
- Cache session and peer resolution.
- Write asynchronously.
- Support dialectic supplement context separately from direct recall.
- Mirror explicit user-memory writes into the provider as conclusions.

Recommendation for praesidium:

- Start with explicit local memory and session search.
- Add provider-style abstraction before adding a Honcho-like system.
- If building user modeling, make the representation inspectable and editable.
- Keep the "why did we recall this?" trail available for debugging.

## Procedural Memory: Skills

Skills are Hermes' most important self-improvement mechanism.

A skill is a directory with a `SKILL.md` file and optional support files:

- `references/`
- `templates/`
- `scripts/`
- `assets/`

The system prompt includes only compact skill metadata. The full skill is loaded
on demand. This is progressive disclosure, and it is essential for scale.

### Skill Discovery

Hermes builds a compact skill index from local and external skill directories.
It caches this index in memory and on disk. Local skills take precedence over
external skills. Platform constraints and disabled skill names are part of the
cache key.

This prevents the prompt from ballooning as the number of skills grows.

Recommendation for praesidium:

- Never put full skill bodies in the default prompt.
- Put only name, description, categories, and maybe trigger hints in the prompt.
- Require an explicit `skill_view` or equivalent before using detailed
  instructions.

### Skill Mutation

The `skill_manage` tool can:

- create a skill
- patch a skill
- edit a skill
- delete a skill
- write support files
- remove support files

It validates names, categories, content, frontmatter, paths, and size. It
clears the skill prompt cache after mutation.

It also records provenance:

- Background-review-created skills are marked as agent-created.
- Patches bump patch counters.
- Deletes remove telemetry.

This distinction is critical. The curator only manages agent-created skills,
not bundled or hub-installed skills.

Recommendation for praesidium:

- All skill writes should go through a typed API.
- Every write should record origin, actor, task/session, reason, and source
  transcript reference.
- Separate user-authored, bundled, imported, and agent-created skills.
- Make agent-created skills easy to inspect and easy to archive.

### Skill Quality

Hermes encourages skills to capture:

- repeated procedural knowledge
- user corrections
- workflow-specific pitfalls
- tool-use patterns
- class-level procedures

It discourages skills for:

- transient environment failures
- one-off task facts
- project history
- broad negative claims like "never use tool X"

This is the correct boundary. Skills should encode reusable procedures, not
diary entries.

Recommendation for praesidium:

- Give reviewers a strict taxonomy:
  - memory fact
  - user preference
  - project convention
  - procedural skill
  - task history
  - no durable update
- Require a reason when creating or patching a skill.
- Prefer patching existing umbrella skills before creating narrow new skills.

## Background Review

Hermes' background review is the heart of its self-improvement loop.

After a turn, the parent agent can fork a review agent. The reviewer receives a
snapshot of the transcript and a prompt asking whether any memory or skill
should be saved or updated.

Important properties:

- It runs after the visible answer.
- It is best-effort.
- It suppresses normal status noise.
- It disables recursive review.
- It disables external memory ingestion.
- It reuses the parent's built-in memory store.
- It uses a restricted tool whitelist.
- It auto-denies dangerous terminal approvals.
- It can summarize successful self-improvement actions.

The reviewer is not just a summarizer. It is an agent with write tools, but the
write surface is constrained.

### Why This Matters

Foreground agents often do not stop to improve themselves. If self-improvement
is left as an optional part of task completion, it will be inconsistent. A
background reviewer turns "what did we learn?" into a harness responsibility.

For praesidium, this is highly relevant because many agents work through tasks,
handoffs, and service restarts. We should capture learnings at task boundaries,
not rely on the active agent to remember to do it.

### Recommended Review Triggers

Hermes uses periodic nudges. Praesidium should add richer triggers:

- User says "remember", "next time", "don't do that", or corrects the agent.
- Agent completes a wrkq task.
- Agent adds a final task comment.
- Agent performs a manual smoke test after a fix.
- Agent encounters the same error twice.
- Agent escalates because of missing context.
- A subagent returns a useful discovery.
- A cron job or worker produces a durable observation.
- A service restart procedure changes.
- A user overrides an agent's plan.

Periodic review should remain, but event-based review will capture higher-value
learning.

### Recommended Review Output

The reviewer should produce structured decisions:

```yaml
memory_updates:
  - target: user|agent|project
    action: add|patch|remove|none
    text: ...
    evidence:
      session_id: ...
      message_ids: [...]

skill_updates:
  - action: create|patch|none
    skill: ...
    reason: ...
    evidence:
      task_id: ...
      session_id: ...

no_update_reason: ...
```

Then the harness, not the reviewer prose, should apply validated writes.

Hermes lets the reviewer call tools directly. That works, but for praesidium I
would put a typed review-result layer between model judgment and mutation. This
gives us better auditability and easier dry-run modes.

## Skill Curation

Self-improving systems accumulate junk unless there is a cleanup loop. Hermes
has one.

The curator tracks usage state in a sidecar file and periodically reviews
agent-created skills. It can:

- mark unused skills stale
- archive old stale skills
- reactivate stale skills when used
- consolidate overlapping skills
- move narrow content into references/templates/scripts
- rewrite cron job skill references after consolidation
- write reports
- create backups
- restore archived skills
- pin skills to protect them

It does not delete skills outright. It archives them.

This is an excellent default. Autonomous deletion is too risky; archival keeps
rollback cheap.

### Curator Policy

Hermes' curator is intentionally aggressive. It wants umbrella skills and
expects many narrow skills to be archived.

This solves sprawl, but it has risks:

- Useful niche details can become less discoverable.
- Consolidated umbrella skills can become too broad.
- References may not be loaded unless the umbrella skill points clearly to
  them.
- Agents may lose trigger precision if descriptions become generic.

Recommendation for praesidium:

- Start conservative.
- Archive unused narrow skills, but require stronger evidence before
  consolidation.
- Generate a human-readable curation report every run.
- Keep rollback one command away.
- Support pinning from day one.
- Never curate user-authored or bundled skills automatically.

## Episodic Memory: Session Search

Hermes stores sessions and messages in SQLite and exposes `session_search`.

The current implementation is FTS-based. It supports:

- query-based discovery
- recent session browsing
- scrolling around a message
- session lineage awareness
- exclusion of current session lineage
- context snippets and bookends

The README language about LLM summarization appears stale. The actual tool is a
deterministic search and browsing tool, not a semantic summarizer.

This is still valuable. It gives agents a cheap way to answer:

- "What did we do last time?"
- "Where did I see this error?"
- "Which session contained that decision?"
- "What was the previous command or task?"

Recommendation for praesidium:

- Use wrkq, hrc logs, acp messages, and session transcripts as episodic memory.
- Start with lexical search and lineage filters.
- Add semantic retrieval later for paraphrase recall.
- Require agents to search episodic memory before making claims about prior
  decisions.
- Keep session search separate from durable user/profile memory.

## Delegation and Subagents

Hermes supports subagents through `delegate_task`.

Child agents run in isolated fresh conversations. The parent sees only a result
summary. Children default to blocked tools like memory, clarification,
delegation, message sending, and code execution unless configured otherwise.

Useful design choices:

- Isolated child context prevents transcript bloat.
- Parent controls concurrency and timeout.
- Spawn depth is capped.
- Child role can be leaf or orchestrator.
- Parent memory provider can observe delegation results.
- Progress events are surfaced to the TUI or gateway.

Risk:

- Because child agents generally run with `skip_memory=True`, useful discoveries
  can be lost unless the parent summary captures them or the provider delegation
  hook records them.

Recommendation for praesidium:

- Treat subagent completion as a self-improvement review trigger.
- Require child result summaries to include:
  - durable findings
  - reusable procedures
  - failed assumptions
  - commands that actually worked
  - references to files/tasks/logs
- Feed child results into the same review pipeline as user turns.

## Programmatic Tool Calling

Hermes has an `execute_code` tool that lets the model write Python scripts that
call Hermes tools through generated RPC helpers.

This is not self-improvement by itself, but it matters because it lowers the
cost of multi-step investigations. Instead of spending many model turns calling
tools one by one, the agent can write a short script that performs a structured
workflow and returns compact output.

Relevant benefits:

- Less context churn.
- Fewer model round trips.
- More reproducible tool sequences.
- Natural bridge to reusable scripts inside skills.

Recommendation for praesidium:

- Use programmatic tool workflows for repeatable diagnostics.
- Promote successful scripts into skill support files.
- Require scripts promoted to skills to have names, inputs, expected outputs,
  and safety notes.

## Cron, Webhooks, Kanban, and Routines

Hermes treats scheduled and event-triggered work as first-class agent contexts.

Cron jobs can:

- run scripts
- load specific skills
- use provider/model overrides
- set workdirs
- include prior job output
- deliver results
- run without an agent if script-only

Webhook routes can:

- receive external events
- render prompt templates
- load skills
- deliver results back to GitHub or other gateway platforms

Kanban gives a durable worker/task surface.

These are important for self-improvement because agents learn from recurring
work. A scheduled job that fails every night should become a skill update or
runbook patch. A webhook that handles repeated repository events should
accumulate procedural knowledge.

Hermes generally uses `skip_memory=True` for cron-like contexts to avoid
corrupting user memory with automated output. That is a good default, but it
means cron discoveries need an explicit review path if they should become
durable.

Recommendation for praesidium:

- Run background automation with memory writes disabled by default.
- Emit structured observations from jobs.
- Review observations separately.
- Track which skills each job used.
- Treat repeated job failures as skill-review triggers.

## Context Compression

Hermes includes a context engine abstraction and a default compressor.

Compression is not exactly self-improvement, but it affects long-running
learning. A compressor decides what task state survives when context is too
large. Hermes distinguishes compression summaries from authoritative durable
memory.

That distinction is important. A compression summary should help continue the
current session; it should not become cross-session truth without review.

Recommendation for praesidium:

- Keep compression summaries separate from memory.
- Allow memory providers to hook pre-compression.
- Never treat a compression summary as a skill or profile update by default.
- Save compression artifacts for audit and session replay.

## Safety and Integrity Mechanisms

Hermes includes several practical safeguards:

- Threat scanning for memory writes and loaded memory entries.
- Prompt-injection scanning for cron prompts and skill-loaded prompts.
- File locks and atomic writes for memory.
- Drift detection before overwriting memory files.
- Tool whitelists for background review.
- Disabled recursive review.
- Dangerous terminal approval denial in review.
- Agent-created skill provenance.
- Pinned skills.
- Archive instead of delete.
- Backup and rollback for curation.
- Prompt snapshot persistence for reproducibility.

These are all worth copying.

Praesidium should add:

- Signed or checksummed skill revisions.
- Per-write evidence references.
- A review queue for high-impact skill changes.
- Policy levels: observe, propose, apply.
- A local diff viewer for skill and memory writes.
- Health reporting for external memory providers.
- Evaluation hooks to prove a new skill helps.

## Most Relevant Features for Praesidium

### 1. Post-Task Reflection Worker

This is the highest-value feature to build.

Praesidium already has tasks, agents, hrc sessions, acp messages, and service
lifecycles. The right reflection boundary is often task completion, not chat
turn completion.

The reflection worker should inspect:

- task description and final comment
- transcript excerpts
- commands run
- files changed
- smoke test results
- user corrections
- subagent outputs
- related errors/logs

It should decide whether to:

- write user memory
- write agent memory
- write project memory
- create or patch a skill
- update a runbook
- do nothing

### 2. Typed Durable Learning Objects

Do not let "memory" become one bucket.

Recommended object types:

- `user_fact`: stable user preference or identity fact.
- `agent_fact`: durable agent operating preference.
- `project_fact`: convention or environment fact scoped to a project.
- `procedure`: reusable skill or runbook.
- `episode`: searchable task/session history.
- `evaluation`: evidence that a procedure worked.
- `exception`: known caveat or anti-pattern with evidence.

Each object should have:

- id
- scope
- owner
- source session/task
- created_by
- created_at
- updated_at
- confidence
- evidence links
- state: active, stale, archived, rejected

### 3. Skills as First-Class Assets

Skills should not be loose markdown only. Markdown is a good authoring format,
but the system needs metadata and lifecycle state.

Minimum skill metadata:

- name
- description
- version
- scope: global, project, agent, user
- created_by: user, agent, imported, bundled
- source task/session
- pinned
- state
- last_used_at
- use_count
- patch_count
- validators
- related skills

Skill support files should be allowed, but constrained:

- references
- templates
- scripts
- assets

### 4. Curator with Reports and Rollback

Do not ship autonomous skill creation without curation.

The curator should:

- detect unused agent-created skills
- detect overlapping skills
- suggest consolidation
- archive stale skills
- never delete by default
- never mutate user-authored or bundled skills automatically
- write a report
- make rollback easy

Initial mode should probably be "propose" rather than "apply". Once reports are
trusted, enable automatic archival of obviously unused agent-created skills.

### 5. Session and Task Search

Hermes session search should map naturally onto praesidium:

- wrkq tasks
- comments
- hrc transcripts
- acp messages
- service logs
- handoffs

Build lexical search first. It will catch task IDs, filenames, commands, error
strings, project names, and function names. Add embeddings later.

Agents should be explicitly instructed to search before saying:

- "I do not know"
- "we have not done that"
- "there is no precedent"
- "this is new"

### 6. Explicit Learning Events

The harness should emit learning events instead of hoping reviewers infer
everything from transcripts.

Useful event types:

- `user_correction`
- `task_completed`
- `manual_smoke_test_passed`
- `manual_smoke_test_failed`
- `repeated_tool_failure`
- `service_restart_required`
- `subagent_discovery`
- `new_project_convention`
- `skill_used`
- `skill_failed`
- `skill_patched`
- `memory_written`
- `curator_archived_skill`

These events can feed review and search.

## Proposed Praesidium Blueprint

### Storage

Use local-first storage with clear scopes.

Suggested layout:

```text
~/praesidium/var/agents/<agent>/memory/
  USER.md
  AGENT.md
  PROJECTS/<project>.md

~/praesidium/var/agents/<agent>/skills/
  <skill-name>/SKILL.md
  <skill-name>/references/
  <skill-name>/templates/
  <skill-name>/scripts/

~/praesidium/var/state/self-improvement.sqlite
  learning_events
  memory_objects
  skill_usage
  skill_revisions
  review_runs
  curator_runs
  evidence_links
```

wrkq should remain the task source of truth. The self-improvement database
should link to wrkq tasks rather than duplicating them.

### Review Pipeline

Recommended pipeline:

1. Harness emits learning event.
2. Review scheduler batches or immediately runs based on event priority.
3. Reviewer receives transcript/task/search context.
4. Reviewer emits structured proposed writes.
5. Validator checks schema, scope, paths, size, and policy.
6. Applies safe writes automatically, queues risky writes.
7. Writes audit record and links evidence.
8. Optionally comments back on the wrkq task.

High-confidence automatic writes:

- append project convention from explicit user correction
- append command that fixed a repeated local setup issue
- patch an agent-created skill with a narrow correction
- mark a skill as failed for a specific condition

Require approval or queued review:

- delete/archive a heavily used skill
- change user profile identity facts
- create broad behavioral rules
- create shell scripts with side effects
- alter bundled/system skills

### Skill Lifecycle

Recommended lifecycle:

```text
proposed -> active -> stale -> archived
              ^        |
              |        |
              +--------+
```

Transitions:

- `proposed -> active`: validation passes, or user approves.
- `active -> stale`: no use for a threshold period.
- `stale -> active`: skill is used or patched.
- `stale -> archived`: no use after longer threshold.
- `archived -> active`: manual restore or strong automated evidence.

Never hard-delete by default.

### Review Modes

Use three modes:

- `observe`: reviewer writes reports only.
- `propose`: reviewer writes pending changes.
- `apply`: reviewer applies safe changes directly.

Start in `propose` for skills and `apply` for low-risk project memory.

### Evidence Requirements

Every durable update should answer:

- What changed?
- Why is it durable?
- What evidence supports it?
- What scope does it apply to?
- When should it expire or be reconsidered?

Without evidence, the update should not be applied.

## Design Choices to Copy Directly

Copy these ideas closely:

- Separate declarative, procedural, and episodic memory.
- Keep full skills out of the default prompt.
- Load skills by progressive disclosure.
- Use post-response background review.
- Restrict review tools.
- Disable recursive self-improvement.
- Disable external memory ingestion during review.
- Treat scheduled jobs and subagents as separate harness contexts.
- Track skill usage and provenance.
- Archive rather than delete.
- Pin skills.
- Generate curator reports.
- Store exact system prompts per session.
- Keep memory provider failures nonfatal.
- Sanitize retrieved memory before saving or displaying it.

## Design Choices to Modify

I would adjust these for praesidium:

### Use Typed Proposed Writes Before Mutation

Hermes lets the background reviewer call write tools directly. That is simple
and powerful. For praesidium, use a structured proposal object first, then a
validator/applier. This gives stronger auditability and dry-run support.

### Prefer Task-Boundary Review

Hermes reviews by user-turn and tool-iteration counters. Praesidium should also
review at wrkq task lifecycle boundaries, especially before marking completed.

### Make Skill Creation More Conservative

Hermes' background prompt encourages frequent skill updates. Praesidium should
prefer patching existing skills and project memory before creating new skills.

### Add Evaluation Hooks

A skill should be able to declare a validator:

- command smoke test
- static check
- expected file path
- example invocation
- required environment

This lets the curator distinguish useful skills from plausible prose.

### Make Provider Health Visible

Hermes treats external memory provider failures as nonfatal. That is correct,
but praesidium should surface degraded provider health in status commands.

### Attach Expiration and Confidence

Some learned facts should decay. Add `confidence` and optional `expires_at` to
memory objects and skill caveats.

## Failure Modes to Guard Against

### Memory Poisoning

A malicious or accidental instruction can be stored as durable memory.

Mitigations:

- threat scanning
- source evidence
- scoped memory
- human review for behavioral rules
- ability to list and remove memory entries

### Skill Sprawl

Autonomous creation can produce many narrow, overlapping skills.

Mitigations:

- prefer patching
- skill similarity checks
- curator
- archive instead of delete
- pinning

### Overgeneralization

The agent may turn one bad incident into a global rule.

Mitigations:

- evidence requirements
- scope fields
- confidence
- reviewer prompt examples
- periodic curation

### Stale Procedures

Tools and repos change. Old skills become wrong.

Mitigations:

- use counts
- last validated timestamp
- validators
- failure events
- stale state

### Invisible Background Mutation

Users may be surprised if the agent changes skills silently.

Mitigations:

- compact review status
- reports
- audit log
- diff command
- propose mode

### Context Pollution

Retrieved memory can be copied into transcripts, then later re-ingested.

Mitigations:

- memory-context wrappers
- sanitization before sync
- streaming scrubber
- source labels

### Subagent Knowledge Loss

Child agents may discover useful facts that are lost in a short parent summary.

Mitigations:

- structured child result fields
- delegation review hook
- child transcript search
- parent review trigger

### Curation Damage

The curator can archive or consolidate useful skills.

Mitigations:

- dry-run reports
- conservative thresholds
- pinning
- rollback
- archive only
- never mutate user/bundled skills automatically

## Recommended MVP

Build the smallest useful praesidium self-improvement system in this order.

### Phase 1: Evidence and Search

- Index wrkq tasks, comments, hrc transcripts, and acp messages.
- Add lexical search by task id, file, command, error, and phrase.
- Store exact session/task evidence links.
- Add a command for agents to search prior work.

### Phase 2: Typed Memory

- Add scoped memory stores: user, agent, project.
- Add typed write API with evidence references.
- Add list/read/remove commands.
- Add prompt-injection scanning.
- Add memory snapshots to session prompts.

### Phase 3: Skills

- Define skill directory format.
- Add skill index and `skill_view`.
- Add skill usage tracking.
- Add `skill_manage` with create/patch only at first.
- Record provenance and task/session evidence.

### Phase 4: Reflection Worker

- Run review on task completion and explicit correction events.
- Output structured proposed writes.
- Validate and apply low-risk memory.
- Queue skill changes in propose mode.
- Comment review summary on the wrkq task.

### Phase 5: Curator

- Add stale/archive transitions for agent-created skills.
- Add reports and rollback.
- Add pin/unpin.
- Add conservative consolidation proposals.

### Phase 6: Automation Integration

- Feed subagent results, cron outputs, service restarts, and worker failures into
  the learning event stream.
- Promote repeated successful scripts into skill support files.
- Track skill failures and validator results.

## Example Praesidium Review Prompt Shape

The reviewer should be boring, structured, and evidence-oriented:

```text
You are the praesidium self-improvement reviewer.

Your job is not to continue the task. Your job is to decide whether this
completed work produced durable learning.

Classify each possible update as one of:
- user_memory
- agent_memory
- project_memory
- skill_create
- skill_patch
- episodic_only
- no_update

Only propose updates that are:
- durable beyond this task
- scoped correctly
- supported by concrete evidence
- useful for future agents

Do not create a skill for one-off task history.
Do not create broad rules from transient failures.
Prefer patching an existing skill over creating a new one.

Return structured YAML only.
```

Then a deterministic applier should validate the YAML and perform writes.

## What Hermes Gets Right

Hermes' best ideas are architectural, not cosmetic:

- It treats self-improvement as a harness-level responsibility.
- It separates memory types cleanly.
- It has a procedural memory substrate with support files.
- It uses background review rather than relying only on foreground discipline.
- It has lifecycle management for agent-created procedures.
- It preserves prompt-cache stability.
- It makes external memory pluggable.
- It gives scheduled and delegated work distinct harness contexts.

These are the pieces most worth importing.

## What Hermes Leaves Open

Hermes still leaves several areas for a stricter system to improve:

- Stronger typed proposals before writes.
- Better evaluation of whether a skill improved outcomes.
- More visible provider-health reporting.
- More conservative skill creation defaults.
- Semantic recall in addition to lexical session search.
- Richer event triggers beyond periodic nudges.
- Expiration/confidence for learned facts.
- More explicit user consent levels for autonomous skill changes.

These are good opportunities for praesidium to exceed the Hermes design while
keeping the same core insight.

## Final Recommendation

Build praesidium self-improvement around a durable learning pipeline:

```text
event -> evidence collection -> reviewer -> typed proposal -> validator
      -> memory/skill write -> audit log -> curator -> future prompt/search
```

Keep the write APIs narrow, the audit trail strong, and the object model
separated by memory type. Let agents learn procedures, but make every durable
procedure inspectable, scoped, versioned, and reversible.

The highest-leverage first implementation is a post-wrkq-task review worker
that can propose project memory and skill patches from completed work. That
would immediately turn everyday agent work into a compounding asset without
requiring a full external memory platform on day one.
