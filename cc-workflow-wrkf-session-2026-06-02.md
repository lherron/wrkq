# CC Workflow ↔ wrkf — Session Transcript

- **Source:** `/Users/lherron/.claude/projects/-Users-lherron-praesidium-agent-control-plane/570c544d-5476-458c-b3a9-9c2bd4d919d1.jsonl`
- **Session ID:** 570c544d-5476-458c-b3a9-9c2bd4d919d1
- **Project cwd:** ~/praesidium/agent-control-plane
- **Rendered:** 2026-06-04 19:16

---


## 🤖 Assistant · 2026-06-02 07:26

clod ready in agent-control-plane (primary). What do you need?

## 🧑 User · 2026-06-02 07:27

Study the workflow tool and available skills/guides and tell me how it works.

## 🤖 Assistant · 2026-06-02 07:27

I'll study the Workflow tool's spec plus any workflow definitions/guides in the repo, then explain how it works.

## 🤖 Assistant · 2026-06-02 07:27

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "echo \"=== .claude/workflows dirs ===\"; find ~/praesidium -type d -name workflows 2>/dev/null | head -20; echo \"=== workflow script files ===\"; find ~/praesidium -path '*/workflows/*' -name '*.js' 2>/dev/null | head -20; echo \"=== session workflow scripts ===\"; ls -la ~/.claude/projects/-Users-lherron-praesidium-agent-control-plane/ 2>/dev/null | head",
  "description": "Find workflow definitions and dirs"
}
```
</details>

## 🤖 Assistant · 2026-06-02 07:27

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "echo \"=== any docs mentioning Workflow tool / orchestration ===\"; grep -rl -i \"ultracode\\|Workflow(\" ~/praesidium/agent-control-plane --include=*.md 2>/dev/null | head; echo \"=== guides dirs ===\"; find ~/praesidium/var/agents/clod/skills -maxdepth 2 -name 'SKILL.md' 2>/dev/null | head -40",
  "description": "Search for workflow guides/docs"
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 07:27</summary>

```
=== any docs mentioning Workflow tool / orchestration ===
(eval):1: no matches found: --include=*.md
=== guides dirs ===
/Users/lherron/praesidium/var/agents/clod/skills/discord-virtu/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/acp-task-workflow/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/broker-tmux-ghostmux-e2e/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/ingest-source/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/acp-server-ops/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/agent-tasker/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/ghoste2e/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/thermo-nuclear-review/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/wrkf-debug/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/refactor-analysis/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/wrkf-tasker/SKILL.md
/Users/lherron/praesidium/var/agents/clod/skills/wrkf-authoring/SKILL.md
```
</details>

<details><summary>🔧 tool result · 2026-06-02 07:27</summary>

```
=== .claude/workflows dirs ===
/Users/lherron/praesidium/agent-spaces/.github/workflows
/Users/lherron/praesidium/agentchat/.github/workflows
/Users/lherron/praesidium/workboard/.github/workflows
/Users/lherron/praesidium/hrc-runtime/.github/workflows
/Users/lherron/praesidium/agent-control-plane/.github/workflows
/Users/lherron/praesidium/hrc-ios/.github/workflows
/Users/lherron/praesidium/scriptable-ghostty/.github/workflows
/Users/lherron/praesidium/agent-spaces/node_modules/json-schema-traverse/.github/workflows
/Users/lherron/praesidium/taskboard/.github/workflows
/Users/lherron/praesidium/agent-spaces/node_modules/fast-uri/.github/workflows
/Users/lherron/praesidium/agent-spaces/node_modules/ast-types/.github/workflows
/Users/lherron/praesidium/taskboard/node_modules/codemirror/.github/workflows
/Users/lherron/praesidium/taskboard/node_modules/reusify/.github/workflows
/Users/lherron/praesidium/taskboard/node_modules/@ungap/structured-clone/.github/workflows
/Users/lherron/praesidium/taskboard/node_modules/@codemirror/commands/.github/workflows
/Users/lherron/praesidium/taskboard/node_modules/.pnpm/@codemirror+lang-javascript@6.2.4/node_modules/@codemirror/lang-javascript/.github/workflows
/Users/lherron/praesidium/taskboard/node_modules/.pnpm/reusify@1.1.0/node_modules/reusify/.github/workflows
/Users/lherron/praesidium/taskboard/node_modules/.pnpm/codemirror@6.0.2/node_modules/codemirror/.github/workflows
/Users/lherron/praesidium/taskboard/node_modules/.pnpm/@codemirror+lint@6.9.2/node_modules/@codemirror/lint/.github/workflows
/Users/lherron/praesidium/taskboard/node_modules/.pnpm/@codemirror+state@6.5.4/node_modules/@codemirror/state/.github/workflows
=== workflow script files ===
=== session workflow scripts ===
total 1344
drwxr-xr-x@ 11 lherron  staff     352 Jun  2 07:26 .
drwxr-xr-x@ 82 lherron  staff    2624 May 31 11:44 ..
drwxr-xr-x@  3 lherron  staff      96 Mar 25 10:47 03306a60-fda9-46dd-9f31-35fedace89f3
drwxr-xr-x@  3 lherron  staff      96 Mar 25 09:16 364b2ab8-8120-48fd-a8e6-5603bf7e061e
drwxr-xr-x@  3 lherron  staff      96 Mar 25 08:33 37ab1980-b18e-4feb-a7ae-32adf34dde26
drwxr-xr-x@  3 lherron  staff      96 Mar 25 10:47 4dc117c0-9597-42d9-bf43-69b272d465f0
-rw-------@  1 lherron  staff   38514 Jun  2 07:27 570c544d-5476-458c-b3a9-9c2bd4d919d1.jsonl
drwxr-xr-x@  3 lherron  staff      96 Mar 31 20:01 69f2bd89-d1fc-42c0-9853-f938c9cbae6d
-rw-------@  1 lherron  staff  646410 May 25 21:49 b4bc4466-f657-4099-8eaf-a50570fda250.jsonl
```
</details>

## 🤖 Assistant · 2026-06-02 07:27

No project-specific workflow scripts exist here (the only `workflows/` dirs are GitHub Actions, unrelated). So the **Workflow tool** in this environment is the built-in deterministic multi-agent orchestrator described in its tool spec, and the "skills/guides" are the ones in my registry + `~/praesidium/var/agents/clod/skills/`. Here's how it all fits together.

## What the Workflow tool is

It runs a **JavaScript orchestration script** that spawns and coordinates subagents deterministically. The *control flow* (loops, fan-out, conditionals, barriers) is plain JS you write; the *work* inside each step is done by a subagent (an `agent()` call). It runs **in the background** — the tool call returns a `runId` immediately and a `<task-notification>` fires when it finishes. Progress is watchable via `/workflows`.

The point isn't just parallelism — it's structure: decompose to be **comprehensive**, get independent perspectives + adversarial checks to be **confident**, or take on **scale** one context can't hold (migrations, audits, sweeps).

## When it's allowed to fire (gating)

This is a hard rule, not a style preference. I only call Workflow when the user **explicitly opts in**, via one of:
- the keyword **`ultracode`** in the prompt, or ultracode is on for the session;
- a direct ask in the user's own words ("use a workflow", "fan out agents", "orchestrate with subagents");
- a skill/slash-command whose instructions tell me to call it;
- a request to run a specific named/saved workflow.

For anything else — even tasks that *would* benefit — I use individual `Agent` calls or just describe what a workflow could do and ask. Workflows can spawn dozens of agents and burn a lot of tokens, so the scale has to be requested.

## The script shape

Every script starts with a **pure-literal** `meta` block (no variables/calls/interpolation), then the body:

```js
export const meta = {
  name: 'review-changes',
  description: 'Review changed files, verify each finding',  // shown in permission dialog
  phases: [{ title: 'Review' }, { title: 'Verify' }],         // one entry per phase()
}
phase('Review')
const r = await agent('…', { schema: FINDINGS_SCHEMA })
```

It's plain JS, **not TypeScript** (no type annotations/generics). Runs in an async context so `await` is top-level. `Date.now()`/`Math.random()`/argless `new Date()` are **banned** (they'd break resume) — pass timestamps via `args`, vary randomness by index. No filesystem/Node APIs, but standard built-ins work.

## The primitives

| Hook | What it does |
|---|---|
| `agent(prompt, opts?)` | Spawn a subagent. Returns its final **text** by default; with `opts.schema` (JSON Schema) it's forced to return a **validated object**. `opts`: `label`, `phase`, `schema`, `model`, `isolation:'worktree'`, `agentType`. Returns `null` if skipped → `.filter(Boolean)`. |
| `pipeline(items, ...stages)` | **Default** for multi-stage work. Each item flows through all stages independently — **no barrier**. Item A can be in stage 3 while B is still in stage 1. Wall-clock = slowest single chain. Stages get `(prev, original, index)`. |
| `parallel(thunks)` | **Barrier**: awaits all thunks before returning. A throwing thunk resolves to `null` (call never rejects) → filter. Use only when you need *all* results together. |
| `phase(title)` | Groups subsequent agents under a progress group. |
| `log(msg)` | Narrator line above the progress tree. |
| `args` | The `args` value passed to the tool, verbatim (parameterize named workflows). |
| `budget` | `{ total, spent(), remaining() }` — the turn's token target. Hard ceiling; `agent()` throws once exhausted. For budget-scaled loops. |
| `workflow(name\|{scriptPath}, args?)` | Run another workflow inline as a sub-step (one level deep only). |

## Execution model

- **pipeline vs parallel** is the central decision. A barrier (`parallel`) is justified *only* when stage N needs cross-item context from all of stage N-1: dedup/merge across the full set, early-exit on zero, or "compare against the other findings." It is **not** justified by "I need to flatten/map/filter first" (do that inside a pipeline stage) or "it's cleaner." When in doubt → `pipeline`.
- **Concurrency cap**: `min(16, cores-2)` agents run at once; excess queues. Lifetime cap of 1000 agents (runaway backstop). So you can pass 100 items to `pipeline`/`parallel` and they all complete, ~10–14 at a time.
- **Structured output**: schema validation happens at the tool-call layer, so the subagent retries on mismatch — `agent()` returns clean data, no parsing.

## Quality patterns (compose freely)

- **Adversarial verify** — N skeptics per finding prompted to *refute*; kill on majority.
- **Perspective-diverse verify** — give each verifier a distinct lens (correctness/security/perf/repro) instead of N identical refuters.
- **Judge panel** — N independent attempts from different angles, scored, synthesize from the winner.
- **Loop-until-dry** — keep spawning finders until K consecutive rounds find nothing new (dedup against `seen`, not `confirmed`, or it never converges).
- **Multi-modal sweep** — agents each searching a different way (by-container/content/entity/time).
- **Completeness critic** — a final agent asking "what's missing?" feeds the next round.
- **No silent caps** — `log()` anything dropped (top-N, sampling) so truncation doesn't read as full coverage.

Scale to the ask: "find any bugs" → few finders, single-vote; "thoroughly audit" → big finder pool + 3–5-vote adversarial pass + synthesis.

## Resume & iteration

Each invocation **persists its script to disk** and returns the path. To iterate: `Edit` that file, re-invoke with `{ scriptPath }`. To resume after a stop/edit: `{ scriptPath, resumeFromRunId }` — the longest unchanged prefix of `agent()` calls returns cached instantly; the first changed call onward re-runs live. Same script + same args = 100% cache hit. (This is why the date/random ban exists.)

## How this ties to the skills/guides here

No saved named workflows exist in this repo, so today the tool is driven inline by skills that embed orchestration:
- **`deep-research`** — fan-out web searches → fetch → adversarially verify → cited synthesis (textbook multi-modal sweep + verify).
- **`code-review` ultra** / **`thermo-nuclear-review`** — the find→dedup→diverse-lens→loop-until-dry review shape, run in the cloud (user-triggered/billed; I can't launch ultrareview myself).
- **`defaults:sub10`** — simpler 10-agent fan-out.
- The `clod-agent` skills (`agent-tasker`, `wrkf-tasker`, `acp-task-workflow`) are a *different* orchestration layer — they coordinate **real agent sessions via hrcchat/wrkf**, not in-process `agent()` subagents. Don't conflate the two: Workflow = ephemeral in-context subagents for one turn; agent-tasker/wrkf = persistent cross-agent task coordination across the praesidium collective.

**One-line summary:** Workflow lets me write a JS script that deterministically fans out subagents — `pipeline` by default, `parallel` only at genuine barriers — with structured outputs, token budgets, and resumable caching, and I only reach for it when you've explicitly opted into that scale (e.g. `ultracode`).

Want me to author or dry-run a concrete one against this repo?

## 🧑 User · 2026-06-02 07:53

Expand on agent() opts for the injected method

## 🤖 Assistant · 2026-06-02 07:54

Here's the full `opts` surface for the injected `agent(prompt, opts?)` method inside a Workflow script. Signature:

```js
agent(prompt: string, opts?: {
  label?: string,
  phase?: string,
  schema?: object,
  model?: 'sonnet' | 'opus' | 'haiku',
  isolation?: 'worktree',
  agentType?: string,
}): Promise<any>
```

## The return value (governed by `schema`)
- **No `schema`** → resolves to the subagent's **final text** as a `string`.
- **With `schema`** (a JSON Schema object) → the subagent is *forced* to call a `StructuredOutput` tool, and `agent()` resolves to the **validated object**. Validation happens at the tool-call layer, so the model **retries on mismatch** — you never parse, and you never get malformed data. This is the single biggest lever: prefer `schema` whenever a later stage consumes the result programmatically (filtering, dedup, branching). Free-text returns are for "summarize for a human" leaf nodes.
- **Skipped mid-run** (user skips the agent) → resolves to `null`, *not* a throw. Always `.filter(Boolean)` before using a result set.
- **Errors** → in `parallel`/`pipeline`, a throwing/erroring agent drops to `null` (the wrapping call never rejects); a direct `await agent(...)` that errors *will* reject, so wrap in try/catch if you want to continue.

## `schema`
A JSON Schema literal. Define it once as a const and reuse. Keep it tight — required fields, enums for verdicts, arrays for findings — because the schema *is* the contract the subagent retries against. Example:

```js
const VERDICT = {
  type: 'object',
  required: ['real', 'reason'],
  properties: {
    real: { type: 'boolean' },
    reason: { type: 'string' },
    severity: { type: 'string', enum: ['low','med','high'] },
  },
}
const v = await agent('Refute or confirm this bug: …', { schema: VERDICT })
if (v.real) { … }
```

## `label`
Overrides the display name shown in the `/workflows` progress tree (default is derived from the prompt). Purely cosmetic but **operationally important** for legibility when you fan out — give each agent a stable, scannable label so you can tell at a glance what's running/stuck:

```js
agent(d.prompt, { label: `review:${d.key}` })
agent(verifyPrompt(f), { label: `verify:${f.file}:${f.line}` })
```

## `phase`
Explicitly assigns this agent to a progress group (box) in the display, **overriding** the ambient `phase()` state. This matters because `phase()` mutates a single global — inside `pipeline()`/`parallel()` stages that interleave, the global races and agents land in the wrong box. Inside any fan-out stage, set `phase` explicitly instead of relying on the last `phase()` call:

```js
pipeline(DIMS,
  d => agent(d.prompt,        { phase: 'Review', label: `review:${d.key}` }),
  r => parallel(r.findings.map(f => () =>
        agent(`verify ${f.title}`, { phase: 'Verify', label: `verify:${f.file}` })))
)
```
Same `phase` string → same group box. Use it as the rule (not the exception) once you're past a single linear sequence.

## `model`
Per-agent model override: `'sonnet' | 'opus' | 'haiku'`. **Default to omitting it** — the agent inherits the main-loop / resolved session model, which is almost always what you want. Only set it when you're *highly confident* a tier fits the task (e.g. `haiku` for bulk mechanical classification, `opus` for a hard synthesis/judge step). When unsure, omit. If you do override a phase's model, also note it in that phase's `meta.phases` entry (`{ title, model }`) for display consistency.

## `isolation: 'worktree'`
Runs the agent in a **fresh git worktree** so its file mutations don't collide with sibling agents. **Expensive** — ~200–500ms setup plus disk per agent. Use it *only* when agents **write to files in parallel** and would otherwise conflict (e.g. a migration that edits N files concurrently). The worktree is auto-removed if the agent left it unchanged. For read-only fan-out (review, research, search) — never use it; it's pure overhead.

```js
// parallel code edits that must not clobber each other:
await parallel(files.map(f => () =>
  agent(`Migrate ${f} to the new API`, { isolation: 'worktree', label: `migrate:${f}` })))
```

## `agentType`
Selects a **custom subagent type** instead of the default workflow subagent — resolved from the **same registry as the `Agent` tool** (e.g. `'Explore'`, `'general-purpose'`, `'Plan'`, or a project agent like `'code-reviewer'`). The chosen agent's system prompt/toolset applies. It **composes with `schema`**: the custom agent's prompt gets a `StructuredOutput` instruction appended, so you can still force structured returns. Use `'Explore'` for read-only search fan-out, a reviewer agent for review stages, etc.

```js
agent('Find every call site of deprecatedFoo()', {
  agentType: 'Explore',
  schema: CALLSITES,
  label: 'sweep:callsites',
})
```

## How they combine — canonical call
```js
const result = await agent(dimension.prompt, {
  label: `review:${dimension.key}`,   // legible in /workflows
  phase: 'Review',                    // explicit group (race-safe in fan-out)
  schema: FINDINGS_SCHEMA,            // validated object back, model retries on miss
  agentType: 'code-reviewer',        // specialized prompt/tools
  // model: omitted → inherit session model
  // isolation: omitted → read-only, no worktree needed
})
```

**Mental model:** `schema` controls *what you get back*, `label`/`phase` control *what you see* in the progress tree, `model`/`agentType` control *who does the work*, and `isolation` controls *where it runs* (only when parallel writes would collide). The two you should set on almost every fan-out call are `label` and `phase`; the two you should usually leave alone are `model` and `isolation`.

## 🧑 User · 2026-06-02 10:29

How are worktrees merged?  Is it programmatic/deterministic or agent-managed?

## 🤖 Assistant · 2026-06-02 10:29

Honest answer: **the tool spec does not document any merge.** So I want to separate what's actually specified from what isn't, rather than invent a mechanism.

## What the spec actually guarantees

Only two things about `isolation: 'worktree'`:
1. Each agent runs in a **fresh git worktree** so parallel file mutations don't collide.
2. The worktree is **auto-removed if unchanged**.

That's the entire documented lifecycle. There is **no documented automatic merge-back** of changed worktrees into your primary working tree. "Auto-removed if unchanged" is cleanup, not reconciliation — and notably it only describes the *unchanged* case. The spec is silent on what happens to a worktree that *did* change.

## What that implies (and where I'm inferring)

Given the design, merging is **not programmatic/deterministic by the tool** — it's on you, the orchestrator. The reasoning:

- `agent()` returns **text or a validated object** — not a diff, not a tree, not a merge result. The return channel carries *data*, not *file state*. So whatever an isolated agent wrote to its worktree does not flow back through the return value.
- Worktrees are separate checkouts. Changes made in one live in that directory (and on whatever branch/commit the agent made there); they do **not** appear in the main working directory automatically.
- Therefore reconciliation is **agent/script-managed**, with two viable patterns:
  - **Commit-then-merge:** each isolated agent commits its work on a branch in its worktree; after the workflow returns, *you* (the main loop) merge/rebase/cherry-pick those branches — handling conflicts as a normal git operation. Deterministic only insofar as git's merge is.
  - **Patch-as-data:** each agent returns its changes as a structured diff/patch via `schema`, and a final synthesis stage (or the main loop) applies them to the primary tree deterministically. This keeps the orchestrator in control of conflict resolution.

The tool gives you **collision-free parallel writes during execution** — it does *not* give you a free, deterministic three-way merge at the end. That part is your problem to design.

## Practical consequence

If you fan out N worktree agents that each edit overlapping files, you don't get an auto-resolved result — you get N divergent trees and a reconciliation step you have to author. Worktree isolation is best when the partition is **clean** (agent *i* owns file set *i*, no overlap), so the "merge" is really just "collect N disjoint change-sets." For overlapping edits, prefer a non-isolated sequential stage or a patch-as-data synthesis pass.

## Caveat on confidence

This is inference from the documented behavior + git worktree semantics, not from reading the harness implementation (the Workflow runtime is part of the Claude Code harness, not this repo, so I can't grep it here). If the exact merge/cleanup behavior matters for something you're about to build, the reliable way to pin it down is empirical: run a tiny throwaway workflow with two `isolation:'worktree'` agents that each write a file, then inspect `git worktree list`, the branches, and the primary tree after it returns. I can author that probe if you want ground truth rather than my inference.

## 🧑 User · 2026-06-02 10:33

Look at ‘wrkf —help’ and consider how we might incorporate the concepts from claude code workflows into our wrkf engine.  Source at ../wrkq

Also consider how CC workflow tool could be used to execute a wrkf workflow.

## 🤖 Assistant · 2026-06-02 10:33

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "cd ~/praesidium/wrkq 2>/dev/null && pwd; echo \"=== wrkf --help ===\"; wrkf --help 2>&1 | head -100",
  "description": "Show wrkf help"
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 10:33</summary>

```
/Users/lherron/praesidium/wrkq
=== wrkf --help ===
Workflow engine CLI for wrkq tasks

Usage:
  wrkf [command]

Available Commands:
  check       Run workflow checks
  completion  Generate the autocompletion script for the specified shell
  effect      Inspect and operate workflow effects
  evidence    Add and inspect workflow evidence
  help        Help about any command
  hook        Inspect and debug local hook catalog
  next        
  obligation  Inspect and resolve workflow obligations
  run         Bind actors to workflow runs
  supervisor  Operate recovery and escalation role
  task        Attach and inspect workflow instances on tasks
  transition  
  workflow    Validate, install, and inspect workflow templates

Flags:
      --actor string          Workflow actor id
      --db string             Path to wrkq database file
  -h, --help                  help for wrkf
      --hook-catalog string   Path to wrkf hook catalog JSON (overrides WRKF_HOOK_CATALOG and autodiscovery)
      --json                  Output JSON
      --role string           Workflow role
      --task string           Default task
      --verbose               Verbose output

Use "wrkf [command] --help" for more information about a command.
Shell cwd was reset to /Users/lherron/praesidium/agent-control-plane
```
</details>

## 🤖 Assistant · 2026-06-02 10:33

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "cd ~/praesidium/wrkq 2>/dev/null; echo \"=== tree top ===\"; ls -la; echo \"=== find wrkf source ===\"; find . -path ./node_modules -prune -o -type d -iname '*wrkf*' -print 2>/dev/null | head; find . -path ./.git -prune -o -type f -iname '*wrkf*' -print 2>/dev/null | grep -v node_modules | head -40",
  "description": "Explore wrkq repo layout"
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 10:33</summary>

```
=== tree top ===
total 480
drwxr-xr-x@ 41 lherron  staff   1312 May 31 06:02 .
drwxr-xr-x@ 53 lherron  staff   1696 Jun  1 20:21 ..
-rw-r--r--@  1 lherron  staff      0 Jan 20 20:21 .asp.lock
drwx------@  4 lherron  staff    128 Jan  9 00:41 .claude
-rw-r--r--@  1 lherron  staff   8196 Dec 29 10:48 .DS_Store
lrwxr-xr-x@  1 lherron  staff     57 May 22 06:15 .env.local -> /Users/lherron/.tooling/stacks/env/generated/dev.wrkq.env
drwxr-xr-x@ 18 lherron  staff    576 Jun  2 05:03 .git
-rw-r--r--@  1 lherron  staff   3480 Jun  1 16:55 .gitignore
-rw-r--r--@  1 lherron  staff    203 May 21 14:56 .golangci.yml
-rw-------@  1 lherron  staff   2969 Jan  9 09:56 .goreleaser.yml
-rw-------@  1 lherron  staff    278 Nov 18  2025 .mcp.json
drwxr-xr-x@  3 lherron  staff     96 Nov 19  2025 .playwright-mcp
drwxr-xr-x@ 12 lherron  staff    384 May 28 13:57 .wrkq
-rw-r--r--@  1 lherron  staff   8017 May 22 08:16 AGENTS.md
-rwxr-xr-x@  1 lherron  staff   1044 May 31 06:02 always-clod.sh
drwxr-xr-x@  3 lherron  staff     96 Nov 19  2025 apps
drwxr-xr-x@ 17 lherron  staff    544 Apr  3 11:49 asp_modules
-rw-r--r--@  1 lherron  staff   3954 May 29 06:59 asp-lock.json
-rw-r--r--@  1 lherron  staff   1536 May 21 14:56 asp-targets.toml
drwxr-xr-x@  6 lherron  staff    192 Jun  1 10:45 bin
lrwxr-xr-x@  1 lherron  staff      9 May 22 08:10 CLAUDE.md -> AGENTS.md
drwxr-xr-x@  6 lherron  staff    192 May 21 14:56 cmd
drwxr-xr-x@ 15 lherron  staff    480 Jan  9 10:08 dist
drwxr-xr-x@ 10 lherron  staff    320 May 22 01:11 docs
-rw-r--r--@  1 lherron  staff    414 May 22 07:01 go.mod
-rw-r--r--@  1 lherron  staff   1954 May 22 07:01 go.sum
-rw-r--r--@  1 lherron  staff  11947 May 22 06:35 HANDOFF_IMPL.md
-rwx--x--x@  1 lherron  staff   4117 Nov 19  2025 install.sh
drwxr-xr-x@ 27 lherron  staff    864 May 22 07:01 internal
-rw-r--r--@  1 lherron  staff   8056 May 22 08:16 Justfile
drwxr-xr-x@  4 lherron  staff    128 May 22 08:16 launchd
-rw-r--r--@  1 lherron  staff   1074 Jan  9 09:18 LICENSE
drwxr-xr-x@ 11 lherron  staff    352 Nov 18  2025 mcp-server
-rw-r--r--@  1 lherron  staff   3408 May 25 07:07 README.md
-rw-r--r--@  1 lherron  staff  28292 May 22 00:06 schema_dump.sql
drwx------@  3 lherron  staff     96 Dec  3 10:35 scripts
-rw-r--r--@  1 lherron  staff  65977 May 28 09:58 SELF_IMPROVEMENT.html
-rw-r--r--@  1 lherron  staff  38659 May 28 07:29 SELF_IMPROVEMENT.md
drwx------@ 10 lherron  staff    320 May 21 14:56 test
drwxr-xr-x@ 20 lherron  staff    640 Jan 18 08:28 tmp
-rw-r--r--@  1 lherron  staff   9723 Jan 12 20:17 WRKQ_STATE_MACHINE_SPEC.md
=== find wrkf source ===
./cmd/wrkf
./internal/wrkfcli
./.wrkq/wrkf-agent-tasker
./test/smoke-wrkf.sh
./bin/wrkf
./.wrkq/wrkq.db.pre-wrkf-agent-tasker-20260517T115507Z.bak
./internal/db/migrations/000014_wrkf_schema.sql
Shell cwd was reset to /Users/lherron/praesidium/agent-control-plane
```
</details>

## 🤖 Assistant · 2026-06-02 10:33

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "cd ~/praesidium/wrkq\nfor c in next transition workflow run evidence effect obligation check supervisor task hook; do echo \"===== wrkf $c --help =====\"; wrkf $c --help 2>&1 | head -40; echo; done",
  "description": "Dump all wrkf subcommand help"
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 10:33</summary>

```
===== wrkf next --help =====
Usage:
  wrkf next TASK [flags]

Flags:
  -h, --help   help for next

Global Flags:
      --actor string          Workflow actor id
      --db string             Path to wrkq database file
      --hook-catalog string   Path to wrkf hook catalog JSON (overrides WRKF_HOOK_CATALOG and autodiscovery)
      --json                  Output JSON
      --role string           Workflow role
      --task string           Default task
      --verbose               Verbose output

===== wrkf transition --help =====
Usage:
  wrkf transition TASK TRANSITION [flags]

Flags:
      --check stringArray        Check run id
      --context string           Expected context hash
      --dry-run                  Validate without committing
      --expect-revision int      Expected workflow revision
  -h, --help                     help for transition
      --idempotency-key string   Idempotency key
      --run-checks               Run transition checks before committing

Global Flags:
      --actor string          Workflow actor id
      --db string             Path to wrkq database file
      --hook-catalog string   Path to wrkf hook catalog JSON (overrides WRKF_HOOK_CATALOG and autodiscovery)
      --json                  Output JSON
      --role string           Workflow role
      --task string           Default task
      --verbose               Verbose output

===== wrkf workflow --help =====
Validate, install, and inspect workflow templates

Usage:
  wrkf workflow [command]

Available Commands:
  diff        
  install     
  list        
  show        
  validate    

Flags:
  -h, --help   help for workflow

Global Flags:
      --actor string          Workflow actor id
      --db string             Path to wrkq database file
      --hook-catalog string   Path to wrkf hook catalog JSON (overrides WRKF_HOOK_CATALOG and autodiscovery)
      --json                  Output JSON
      --role string           Workflow role
      --task string           Default task
      --verbose               Verbose output

Use "wrkf workflow [command] --help" for more information about a command.

===== wrkf run --help =====
Bind actors to workflow runs

Usage:
  wrkf run [command]

Available Commands:
  bind        
  fail        
  finish      
  list        
  show        
  start       

Flags:
  -h, --help   help for run

Global Flags:
      --actor string          Workflow actor id
      --db string             Path to wrkq database file
      --hook-catalog string   Path to wrkf hook catalog JSON (overrides WRKF_HOOK_CATALOG and autodiscovery)
      --json                  Output JSON
      --role string           Workflow role
      --task string           Default task
      --verbose               Verbose output

Use "wrkf run [command] --help" for more information about a command.

===== wrkf evidence --help =====
Add and inspect workflow evidence

Usage:
  wrkf evidence [command]

Available Commands:
  add         
  exec        
  list        
  show        
  suggest     

Flags:
  -h, --help   help for evidence

Global Flags:
      --actor string          Workflow actor id
      --db string             Path to wrkq database file
      --hook-catalog string   Path to wrkf hook catalog JSON (overrides WRKF_HOOK_CATALOG and autodiscovery)
      --json                  Output JSON
      --role string           Workflow role
      --task string           Default task
      --verbose               Verbose output

Use "wrkf evidence [command] --help" for more information about a command.

===== wrkf effect --help =====
Inspect and operate workflow effects

Usage:
  wrkf effect [command]

Available Commands:
  ack         
  deliver     
  fail        
  list        
  retry       
  show        

Flags:
  -h, --help   help for effect

Global Flags:
      --actor string          Workflow actor id
      --db string             Path to wrkq database file
      --hook-catalog string   Path to wrkf hook catalog JSON (overrides WRK
…[truncated, 7886 chars]
```
</details>

## 🤖 Assistant · 2026-06-02 10:33

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "cd ~/praesidium/wrkq\necho \"=== internal wrkf-ish dirs ===\"; ls -d internal/*wrkf* internal/workflow* 2>/dev/null; find internal -type d \\( -iname '*wrkf*' -o -iname '*workflow*' \\) 2>/dev/null\necho \"=== agent-tasker template tree ===\"; find .wrkq/wrkf-agent-tasker -type f 2>/dev/null | head -60",
  "description": "Locate wrkf engine internals and template"
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 10:33</summary>

```
=== internal wrkf-ish dirs ===
internal/workflow
internal/wrkfcli
internal/wrkfcli
internal/workflow
=== agent-tasker template tree ===
.wrkq/wrkf-agent-tasker/agent-tasker-feature-request.normalized.workflow.json
.wrkq/wrkf-agent-tasker/real-run-timeline.json
.wrkq/wrkf-agent-tasker/hook-catalog.wrapped.json
.wrkq/wrkf-agent-tasker/real-run-next.json
.wrkq/wrkf-agent-tasker/agent-tasker-feature-request.v3.workflow.json
.wrkq/wrkf-agent-tasker/attach.json
.wrkq/wrkf-agent-tasker/hook-catalog.capture.json
.wrkq/wrkf-agent-tasker/agent-tasker-feature-request.v2.workflow.json
.wrkq/wrkf-agent-tasker/real-run-final-instance.json
.wrkq/wrkf-agent-tasker/real-run-task-id.txt
.wrkq/wrkf-agent-tasker/real-run-effects-after-ack.json
.wrkq/wrkf-agent-tasker/real-run-effects-before-ack.json
.wrkq/wrkf-agent-tasker/hook-catalog.json
.wrkq/wrkf-agent-tasker/real-run-next-after-ack.json
.wrkq/wrkf-agent-tasker/effect-handlers/request_observer_review.sh
.wrkq/wrkf-agent-tasker/scripts/common.sh
.wrkq/wrkf-agent-tasker/scripts/validate_report_ready.sh
.wrkq/wrkf-agent-tasker/scripts/validate_plan_ready.sh
.wrkq/wrkf-agent-tasker/scripts/validate_branch_ready.sh
.wrkq/wrkf-agent-tasker/scripts/validate_workflow_semantics.sh
.wrkq/wrkf-agent-tasker/scripts/classify_error.sh
.wrkq/wrkf-agent-tasker/scripts/validate_architect_verdict.sh
.wrkq/wrkf-agent-tasker/scripts/validate_cleanup.sh
.wrkq/wrkf-agent-tasker/scripts/validate_coordinator_smoke_verified.sh
.wrkq/wrkf-agent-tasker/scripts/validate_red_verified.sh
.wrkq/wrkf-agent-tasker/scripts/validate_impl_verified.sh
.wrkq/wrkf-agent-tasker/scripts/validate_live_smoke.sh
.wrkq/wrkf-agent-tasker/scripts/capture_stdin.sh
.wrkq/wrkf-agent-tasker/scripts/validate_delegated_tasks_recorded.sh
.wrkq/wrkf-agent-tasker/scripts/validate_observer_completion_review.sh
.wrkq/wrkf-agent-tasker/scripts/stdin_file_wrapper.sh
.wrkq/wrkf-agent-tasker/scripts/validate_stacked_terminal.sh
Shell cwd was reset to /Users/lherron/praesidium/agent-control-plane
```
</details>

## 🤖 Assistant · 2026-06-02 10:33

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "cd ~/praesidium/wrkq\necho \"=== internal/workflow files ===\"; ls -la internal/workflow/ 2>/dev/null; echo; echo \"=== internal/wrkfcli files ===\"; ls internal/wrkfcli/ 2>/dev/null\necho \"=== template: top-level keys & states ===\"; python3 - <<'PY'\nimport json,glob\nf=\".wrkq/wrkf-agent-tasker/agent-tasker-feature-request.v3.workflow.json\"\nd=json.load(open(f))\nprint(\"TOP KEYS:\", list(d.keys()))\nfor k in (\"id\",\"name\",\"version\",\"initial\",\"initialState\",\"start\"):\n    if k in d: print(k,\"=\",d[k])\n# states\nstates=d.get(\"states\") or d.get(\"nodes\")\nif isinstance(states,dict):\n    print(\"\\nSTATES:\",list(states.keys()))\nelif isinstance(states,list):\n    print(\"\\nSTATES:\",[s.get('id') or s.get('name') for s in states])\n# transitions\ntr=d.get(\"transitions\")\nif isinstance(tr,list):\n    print(\"\\nTRANSITIONS (%d):\"%len(tr))\n    for t in tr[:40]:\n        print(\"  \",t.get('id'),\"|\",t.get('from'),\"->\",t.get('to'),\"| role=\",t.get('role'),\"| checks=\",[c.get('id') if isinstance(c,dict) else c for c in (t.get('checks') or [])])\nPY",
  "description": "Inspect workflow template structure"
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 10:33</summary>

```
=== internal/workflow files ===
total 248
drwxr-xr-x@  5 lherron  staff    160 May 21 14:56 .
drwxr-xr-x@ 27 lherron  staff    864 May 22 07:01 ..
-rw-r--r--@  1 lherron  staff  46191 May 21 14:56 ledger.go
-rw-r--r--@  1 lherron  staff  57638 May 21 14:56 service.go
-rw-r--r--@  1 lherron  staff  14357 May 21 14:56 types.go

=== internal/wrkfcli files ===
root.go
=== template: top-level keys & states ===
TOP KEYS: ['schemaVersion', 'id', 'version', 'kind', 'description', 'initial', 'roles', 'states', 'evidenceKinds', 'obligationKinds', 'checks', 'transitions', 'stateHooks', 'nextActionModel']
id = agent_tasker_feature_request
version = 3
initial = {'status': 'open', 'phase': 'intake'}

STATES: [None, None, None, None, None, None, None, None, None, None, None, None, None, None, None, None, None, None]

TRANSITIONS (16):
   start_diagnosis | {'status': 'open', 'phase': 'intake'} -> None | role= None | checks= []
   diagnosis_to_planning | {'status': 'active', 'phase': 'diagnosis'} -> None | role= None | checks= []
   planning_to_architect_review | {'status': 'active', 'phase': 'planning'} -> None | role= None | checks= ['plan_ready']
   architect_review_complete | {'status': 'waiting', 'phase': 'architect_review'} -> None | role= None | checks= ['architect_verdict']
   delegated_tasks_recorded | {'status': 'active', 'phase': 'setup_tasks'} -> None | role= None | checks= ['delegated_tasks_recorded']
   branch_ready | {'status': 'active', 'phase': 'branch_ready'} -> None | role= None | checks= ['branch_ready']
   dispatch_reds | {'status': 'active', 'phase': 'dispatch_reds'} -> None | role= None | checks= []
   red_results_terminal | {'status': 'waiting', 'phase': 'red_results'} -> None | role= None | checks= ['stacked_terminal', 'red_verified']
   dispatch_impl | {'status': 'active', 'phase': 'red_verified'} -> None | role= None | checks= []
   impl_results_terminal | {'status': 'waiting', 'phase': 'impl_results'} -> None | role= None | checks= ['stacked_terminal', 'impl_verified']
   enter_live_smoke | {'status': 'active', 'phase': 'green_verified'} -> None | role= None | checks= []
   live_smoke_complete | {'status': 'active', 'phase': 'live_smoke'} -> None | role= None | checks= ['live_smoke_verified', 'cleanup_verified']
   submit_completion_claim | {'status': 'active', 'phase': 'report'} -> None | role= None | checks= ['coordinator_smoke_verified', 'report_ready']
   blocked_resolved | {'status': 'waiting', 'phase': 'blocked'} -> None | role= None | checks= []
   recover_from_error | {'status': 'active', 'phase': 'error'} -> None | role= None | checks= []
   report_complete | {'status': 'waiting', 'phase': 'review_pending'} -> None | role= None | checks= ['observer_review_verdict']
Shell cwd was reset to /Users/lherron/praesidium/agent-control-plane
```
</details>

## 🤖 Assistant · 2026-06-02 10:33

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "cd ~/praesidium/wrkq\npython3 - <<'PY'\nimport json\nd=json.load(open(\".wrkq/wrkf-agent-tasker/agent-tasker-feature-request.v3.workflow.json\"))\nprint(\"ROLES:\", json.dumps(d.get(\"roles\"),indent=1)[:1200])\nprint(\"\\nEVIDENCE KINDS:\", [e.get('id') if isinstance(e,dict) else e for e in d.get(\"evidenceKinds\",[])])\nprint(\"\\nOBLIGATION KINDS:\", [e.get('id') if isinstance(e,dict) else e for e in d.get(\"obligationKinds\",[])])\nprint(\"\\nCHECKS:\")\nfor c in d.get(\"checks\",[]):\n    print(\"  \",c.get('id'),\"-> hook=\",c.get('hook') or c.get('handler') or c.get('run'), \"| kind=\",c.get('kind'))\nprint(\"\\nTRANSITIONS to/effects:\")\nfor t in d.get(\"transitions\",[]):\n    to=t.get('to'); \n    print(\"  \",t.get('id'),\"-> to=\",to,\"| effects=\",[ (e.get('kind') or e.get('type') or e.get('id')) for e in (t.get('effects') or [])], \"| produces=\",t.get('producesEvidence') or t.get('evidence'))\nprint(\"\\nSTATE HOOKS sample:\", json.dumps(d.get(\"stateHooks\"),indent=1)[:800])\nprint(\"\\nNEXT ACTION MODEL:\", json.dumps(d.get(\"nextActionModel\"),indent=1)[:1500])\nPY",
  "description": "Dump roles, checks, effects, nextActionModel"
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 10:33</summary>

```
Exit code 1
ROLES: {
 "coordinator": {
  "defaultActor": "agent:clod",
  "description": "Owns diagnosis, decomposition, dispatch, monitoring, verification, live smoke, report, and recovery. Does not close worker wrkq tasks for assignees.",
  "capabilities": [
   "create_tasks",
   "bind_roles",
   "dispatch_runs",
   "attach_evidence",
   "transition",
   "manage_obligations",
   "recover",
   "escalate",
   "call_supervisor"
  ]
 },
 "architect": {
  "defaultActor": "agent:cody",
  "description": "Pressure-tests plan, resolves architecture/scope questions, and reviews scope drift.",
  "capabilities": [
   "attach_architect_verdict",
   "request_plan_revision"
  ]
 },
 "red_author": {
  "defaultActor": "agent:smokey",
  "description": "Writes failing acceptance tests first and/or final acceptance verification.",
  "capabilities": [
   "attach_red_evidence",
   "close_assigned_task"
  ]
 },
 "implementer": {
  "candidateActors": [
   "agent:larry",
   "agent:curly"
  ],
  "description": "Executes scoped implementation work in task-scoped sessions. Shared worktree rules apply.",
  "capabilities": [
   "edit_code",
   "run_tests",
   "attach_impl_evidence",
   "close_assigned_task"
  ]
 },
 "supervisor"

EVIDENCE KINDS: ['source_spec', 'decomposition_plan', 'architect_verdict', 'branch_evidence', 'dispatch_evidence', 'stacked_terminal', 'red_evidence', 'impl_evidence', 'closure_evidence', 'artifact_verification', 'live_smoke_evidence', 'cleanup_evidence', 'report_evidence', 'recovery_evidence', 'delegated_task_manifest', 'coordinator_runbook', 'coordinator_smoke_execution', 'completion_claim', 'observer_completion_review']

OBLIGATION KINDS: ['assignee_close_task', 'fix_wrong_branch', 'recover_stash_collision', 'restore_environment', 'architect_scope_review', 'follow_up_scope_gap', 'await_coordinator_smoke_execution', 'await_observer_completion_review', 'address_observer_rejection']

CHECKS:
Traceback (most recent call last):
  File "<stdin>", line 8, in <module>
AttributeError: 'str' object has no attribute 'get'
```
</details>

## 🤖 Assistant · 2026-06-02 10:33

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "cd ~/praesidium/wrkq\npython3 - <<'PY'\nimport json\nd=json.load(open(\".wrkq/wrkf-agent-tasker/agent-tasker-feature-request.v3.workflow.json\"))\nprint(\"CHECKS:\", json.dumps(d.get(\"checks\"),indent=1)[:1500])\nprint(\"\\n--- one transition full ---\")\nprint(json.dumps(d[\"transitions\"][9],indent=1)[:1800])\nprint(\"\\nNEXT ACTION MODEL:\", json.dumps(d.get(\"nextActionModel\"),indent=1)[:1800])\nPY",
  "description": "Dump checks, a transition, nextActionModel"
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 10:33</summary>

```
CHECKS: {
 "plan_ready": {
  "type": "hook",
  "exitMap": {
   "0": {
    "verdict": "pass",
    "outcome": "plan_ready"
   },
   "11": {
    "verdict": "fail",
    "outcome": "revise_plan"
   },
   "20": {
    "verdict": "fail",
    "outcome": "missing_plan"
   },
   "50": {
    "verdict": "error",
    "outcome": "supervisor_recovery"
   },
   "*": {
    "verdict": "error",
    "outcome": "supervisor_recovery"
   }
  },
  "hookId": "plan_ready"
 },
 "architect_verdict": {
  "type": "hook",
  "exitMap": {
   "0": {
    "verdict": "pass",
    "outcome": "approved"
   },
   "10": {
    "verdict": "block",
    "outcome": "blocked"
   },
   "20": {
    "verdict": "fail",
    "outcome": "missing_architect_verdict"
   },
   "21": {
    "verdict": "fail",
    "outcome": "revise_plan"
   },
   "*": {
    "verdict": "error",
    "outcome": "supervisor_recovery"
   }
  },
  "hookId": "architect_verdict"
 },
 "delegated_tasks_recorded": {
  "type": "hook",
  "exitMap": {
   "0": {
    "verdict": "pass",
    "outcome": "delegated_tasks_ready"
   },
   "20": {
    "verdict": "fail",
    "outcome": "missing_delegated_tasks"
   },
   "*": {
    "verdict": "error",
    "outcome": "supervisor_recovery"
   }
  },
  "hookId": "delegated_tasks_recorded"
 },
 "branch_ready": {
  "type": "hook",
  "exitMap": {
   "0": {
    "verdict": "pass",
    "outcome": "branch_ready"
   },
   "20": {
    "verdict": "fail",
    "outcome": "missing_branch_evidence"
   },
   "22": {
    "verdict": "fail",
    "outcome":

--- one transition full ---
{
 "id": "impl_results_terminal",
 "from": {
  "status": "waiting",
  "phase": "impl_results"
 },
 "by": [
  "coordinator"
 ],
 "checks": [
  "stacked_terminal",
  "impl_verified"
 ],
 "outcomes": [
  {
   "when": {
    "all": [
     {
      "checkOutcome": {
       "check": "stacked_terminal",
       "is": "turn_final_success"
      }
     },
     {
      "checkOutcome": {
       "check": "impl_verified",
       "is": "green_verified"
      }
     }
    ]
   },
   "to": {
    "status": "active",
    "phase": "green_verified"
   },
   "id": "impl_results_terminal_1_green_verified"
  },
  {
   "when": {
    "checkOutcome": {
     "check": "impl_verified",
     "is": "assignee_not_closed"
    }
   },
   "to": {
    "status": "waiting",
    "phase": "impl_results"
   },
   "effects": [
    {
     "kind": "notify",
     "role": "implementer",
     "reason": "assignee_must_close_task"
    }
   ],
   "id": "impl_results_terminal_2_impl_results"
  },
  {
   "when": {
    "checkOutcome": {
     "check": "stacked_terminal",
     "is": "missing_or_invalid_terminal"
    }
   },
   "to": {
    "status": "waiting",
    "phase": "impl_results"
   },
   "effects": [
    {
     "kind": "notify",
     "role": "implementer",
     "reason": "terminal_result_required"
    }
   ],
   "id": "impl_results_terminal_3_missing_terminal"
  },
  {
   "when": {
    "checkOutcome": {
     "check": "impl_verified",
     "is": "missing_impl_evidence"
    }
   },
   "to": {
    "status": "waiting",
    "phase": "impl_results"
   },
   "effects": [
    {
     "kind": "notify",
     "role": "implementer",
     "reason": "implementation_evidence_required"
    }
   ],
   "id": "impl_results_terminal_4_missing_impl_evidence"
  },
  {
   "when": {
    "checkOutcome": {
     "check": "impl_verified",
     "is

NEXT ACTION MODEL: {
 "rankOrder": [
  "hard_block_recovery",
  "owned_transition_available",
  "missing_evidence_collection",
  "dispatch_ready_worker",
  "wait_for_external_owner",
  "nonblocking_followup"
 ],
 "compactFields": [
  "kind",
  "owner",
  "why",
  "command",
  "blocksOn",
  "expectedState"
 ],
 "ownershipResolution": {
  "deliveryRefTemplate": "{actor}@{project}:{taskId}",
  "laneDefault": "main"
 }
}
Shell cwd was reset to /Users/lherron/praesidium/agent-control-plane
```
</details>

## 🤖 Assistant · 2026-06-02 10:35

I've now got a solid read on both engines. Here's the analysis.

## What wrkf actually is (grounded in the v3 template + engine)

wrkf is a **durable, evidence-gated, multi-actor state machine layered over wrkq tasks**. The pieces:

- **State** = a `{status, phase}` tuple (`{active, planning}`, `{waiting, impl_results}`). The initial state and every transition target is one of these tuples.
- **Transitions** (`internal/workflow/service.go` + template `transitions[]`): `from` state → one of several `outcomes`, each gated by `checks`, restricted to roles via `by:[...]`, branching on `checkOutcome` conditions to different `to` states, optionally emitting `effects`.
- **Checks** = typed hooks that map a validator script's **exit code → `{verdict, outcome}`** via `exitMap` (e.g. `plan_ready`: exit 0→pass/plan_ready, 11→fail/revise_plan, 50→error/supervisor_recovery). Outcomes pick the transition branch.
- **Hooks** = the local catalog of `scripts/validate_*.sh` — deterministic external validators.
- **Evidence** = typed artifacts in an append-only **ledger** (`ledger.go`); `evidenceKinds` names them (`red_evidence`, `coordinator_smoke_execution`, `completion_claim`…). Checks validate evidence.
- **Effects** = side-effects of transitions (`notify role`, `request_observer_review`) with a real delivery lifecycle: `deliver/ack/fail/retry` (at-least-once).
- **Obligations** = outstanding duties that gate progress (`assignee_close_task`, `await_observer_completion_review`) — `satisfy/waive/cancel`.
- **Roles → actors**: `coordinator`(clod), `architect`(cody), `red_author`(smokey), `implementer`(larry/curly), `supervisor`. Transitions are role-gated; `wrkf run bind` binds actors.
- **`nextActionModel`**: a declarative scheduler — `wrkf next` ranks the single best action by `[hard_block_recovery > owned_transition_available > missing_evidence_collection > dispatch_ready_worker > wait_for_external_owner > nonblocking_followup]`, with `deliveryRef = {actor}@{project}:{taskId}`.
- Concurrency safety: `--expect-revision`, `--context` hash, `--idempotency-key` on transitions.

## wrkf vs. CC Workflow — they're duals

| | CC Workflow | wrkf |
|---|---|---|
| Control flow | **imperative JS** (loops, fan-out) | **declarative** state machine (JSON) |
| Lifetime | one background turn, ephemeral | durable; spans days, restarts, agents |
| Actors | in-context subagents (one model) | real persistent agents bound to roles |
| State | in-memory JS | SQLite ledger, revisioned, hash-pinned |
| Gating | schema validation + model retry | exit-code hooks over typed evidence |
| Parallelism | `pipeline`/`parallel` **primitives** | hand-rolled dispatch+wait **phases** |
| Verify | N adversarial subagent votes | single deterministic validator (+ observer role) |
| Failure | thunk → `null` | error state → supervisor, effect retry, obligations |

CC has the *imperative driver and fan-out* but no persistence; wrkf has *durable multi-actor persistence* but the driver is "an LLM eyeballs `wrkf next` and acts." Each is the other's missing half.

---

## Part 1 — Concepts to import from CC Workflow into wrkf

Ranked by value:

**1. First-class fan-out / join states (biggest gap).** Today parallel work is hand-rolled as `dispatch_reds → red_results → dispatch_impl → impl_results` — a coordinator manually spawns N workers and a `waiting` phase barrier-joins them. Borrow CC's `parallel` primitive as a **`map` state type**: a transition declares "spawn one sub-run per item in evidence `delegated_task_manifest`, join when all reach terminal." The engine owns the fan-out/barrier instead of the coordinator's prose.

**2. Pipeline (no-barrier) lanes.** CC's key insight — item A in stage 3 while B in stage 1. wrkf's phase barriers force the slowest worker to gate the whole cohort. Model per-item work as **independent sub-workflow instances (lanes)** that advance on their own; the parent joins only at a genuine barrier (e.g. live_smoke needs all green). This is `pipeline` semantics applied to dispatch.

**3. Schema-validated evidence with retry.** `evidenceKinds` are *named* but (from what I saw) shape isn't enforced at `evidence add`. Borrow CC's schema-with-retry: attach a **JSON Schema to each evidenceKind**, validate at add time, reject malformed evidence the way CC forces a subagent to retry `StructuredOutput`. Tightens the ledger and lets `wrkf evidence suggest` emit the exact schema.

**4. Quorum / adversarial check type.** CC's "N skeptics, majority refute." wrkf checks are single hooks. Add `check.type: "quorum"` that dispatches the same verification to N actors or N lenses (correctness/security/repro) and aggregates `≥majority`. Generalizes the single `observer_completion_review` into a panel for high-stakes transitions.

**5. Loop-until-dry state.** CC keeps spawning finders until K dry rounds. Add a **convergence loop state**: re-enter a discovery phase until a check returns "no new findings" K consecutive times — for audits/sweeps that don't have a fixed phase count.

**6. Budget as an obligation + check.** CC's `budget.remaining()` hard ceiling. Add a `budget` obligation kind and a `within_budget` check that blocks `dispatch_ready_worker` and escalates to supervisor when a run's token/cost ceiling is hit.

**7. `wrkf plan` (read-the-script-before-running).** CC lets you read the deterministic JS before it runs. `wrkf next` is single-step. A `wrkf plan` that walks the template to terminal given current/hypothetical evidence gives the same "see the whole path" affordance, complementing the existing `--dry-run`.

---

## Part 2 — Using the CC Workflow tool to execute a wrkf run

The bridge: **a CC Workflow script becomes the deterministic coordinator** that today is "clod reading `wrkf next` by hand." `wrkf` stays the source-of-truth ledger; the CC script is the conductor loop:

```js
// driver loop — wrkf is the truth, CC Workflow is the deterministic conductor
while (true) {
  const next = JSON.parse(await sh(`wrkf next ${task} --json`))
  if (next.kind === 'workflow_complete') break
  switch (next.kind) {                       // == nextActionModel.rankOrder
    case 'hard_block_recovery':
      await agent(recoverPrompt(next), {phase:'Recover'}); break
    case 'owned_transition_available':
      await sh(`wrkf transition ${task} ${next.transition} --run-checks --expect-revision ${next.rev}`)
      break
    case 'dispatch_ready_worker': {          // ← CC fan-out shines here
      const ev = await agent(workerPrompt(next), {agentType: next.role, schema: EVIDENCE[next.evidenceKind], isolation:'worktree'})
      await sh(`wrkf evidence add ${task} --kind ${next.evidenceKind} --body-file -`, ev)
      break
    }
    case 'wait_for_external_owner': return {paused:true, on:next}  // real agent owns it
  }
}
```

But there's a real impedance mismatch I won't paper over, and it splits into two honest modes:

**Mode A — CC Workflow as single-process coordinator + worker pool (one turn, one model plays all roles).** CC subagents *act as* red_author/implementer/architect within one turn and produce evidence; validator-script checks gate each transition deterministically; the wrkf ledger is the durable audit record. You get CC's imperative fan-out + wrkf's evidence gates. **Loses** the multi-actor accountability wrkf is built for (the "different agents, different incentives" property) — one model is grading its own homework, mitigated only by the deterministic hooks. Good for solo-driven runs that still want an auditable ledger.

**Mode B — CC Workflow as conductor over the *real* collective (higher value).** On `dispatch_ready_worker` the script doesn't `agent()` — it shells `hrcchat dm {deliveryRef}` to dispatch the **real** role agent (larry, smokey), then polls `wrkf next` until that agent's evidence lands. Here CC Workflow replaces *only the

## 🧑 User · 2026-06-02 10:35

[Request interrupted by user]

## 🧑 User · 2026-06-02 10:35

Also check ‘wrkf-tasker’ skill

## 🤖 Assistant · 2026-06-02 10:35

<details><summary>⚙️ tool call</summary>

```
Bash
{
  "command": "cat ~/praesidium/var/agents/clod/skills/wrkf-tasker/SKILL.md",
  "description": "Read wrkf-tasker skill"
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 10:35</summary>

```
<persisted-output>
Output too large (35.7KB). Full output saved to: /Users/lherron/.claude/projects/-Users-lherron-praesidium-agent-control-plane/570c544d-5476-458c-b3a9-9c2bd4d919d1/tool-results/b7ywk9t2u.txt

Preview (first 2KB):
---
name: wrkf-tasker
description: Drive a wrkq task end-to-end through the wrkf agent-tasker workflow as coordinator — attach template, decompose, dispatch implementer/red work, run honest coordinator smoke with fresh hash-pinned artifacts, submit completion claim, route to external observer review, close only after observer accepts. Use this for new implementation work, validation work, refactors, or bug fixes that need the workflow ledger. Replaces the legacy agent-tasker skill.
---

You are the coordinator. wrkf is the ledger; you drive it. Other agents (curly, larry, smokey) do implementation work; observer reviews completion; you decompose, dispatch, observe, and submit.

**This skill replaces [[../agent-tasker/SKILL.md]].** The legacy skill remained correct on dispatch ergonomics and worktree discipline, but had no ledger backing — coordinators self-graded with no validator teeth and no external reviewer. The incident `wfi_t01489_1779020094775291000` (T-01489 @1, closed/completed at rev 13) documents what that gap costs. The five wrkf fixes shipped during T-01489's run (autodiscovery, no-lying-next, await_subordinate_closure, coordinator_smoke + freshness anchors, observer review + effect delivery handlers) are now the contract this skill operates on.

## Coverage map: agent-tasker → wrkf

| agent-tasker concern | wrkf surface | Status |
|---|---|---|
| Assignee closes wrkq task; coordinator does not | `closure_evidence{assigneeClosed:true, coordinatorDidNotClose:true}` validator | ✓ Enforced |
| Study spec | `source_spec` evidence at diagnosis | ✓ Required for `diagnosis_to_planning` |
| Decomposition plan with dependencies + validation criteria | `decomposition_plan{phases, dependencies, validationCriteria}` + `plan_ready` check | ✓ Enforced |
| Architect review | `architect_verdict{verdict:approved|revise|blocked}` + check | ✓ Enforced |
...
</persisted-output>
```
</details>

## 🤖 Assistant · 2026-06-02 10:35

<details><summary>⚙️ tool call</summary>

```
Read
{
  "file_path": "/Users/lherron/.claude/projects/-Users-lherron-praesidium-agent-control-plane/570c544d-5476-458c-b3a9-9c2bd4d919d1/tool-results/b7ywk9t2u.txt",
  "offset": 30,
  "limit": 170
}
```
</details>

<details><summary>🔧 tool result · 2026-06-02 10:35</summary>

```
30	
31	What **agent-tasker carried that wrkf does NOT enforce** (coordinator discipline carry-forwards):
32	- Dispatching the actual hrcchat turn / dm to subordinate handles
33	- Composing dispatch briefs (strict worktree rules block, work-until-done, restart-survival pattern)
34	- Choosing surface (`turn --stacked` vs `dm`)
35	- Reading subordinate replies and on-disk artifacts before recording evidence
36	- Sequential vs parallel impl agents on shared worktree
37	- Recovery from stash-collision incidents
38	- Refresh installed binaries before live smoke
39	- Mid-flight evidence polling
40	- `hrc monitor watch` needs `--follow` (T-01497 — defaults to replay-and-exit)
41	- Hashing artifact files with `shasum -a 256` before referencing in coordinator_smoke_execution
42	
43	These remain coordinator-side discipline; wrkf records that you did them via evidence, but doesn't enforce *how*.
44	
45	## Trigger
46	
47	Use this skill when the user asks you to:
48	- Drive a wrkq task through the workflow ("act as coordinator", "run T-XXXXX through wrkf", "decompose and dispatch", "scenario")
49	- Implement a multi-phase feature, refactor, validation, or bug fix that needs subordinate dispatch + verified closure
50	- Coordinate any work where the agent-tasker workflow's phases (intake → diagnosis → planning → architect_review → setup_tasks → dispatch_reds → red → dispatch_impl → impl → live_smoke → report → review → closed) makes sense as the spine
51	
52	Do NOT use this skill for:
53	- Single-step direct-execution work (coordinator-clod fixing a bug yourself) — that's an implementer agent's job (curly/larry), and you should dispatch instead. See [[../../../../../.claude/projects/-Users-lherron-praesidium-agent-spaces/memory/feedback_dispatch_implementer_work.md]].
54	- One-off dispatches that don't need the workflow audit trail — direct `hrcchat dm <handle>` is fine.
55	
56	## Setup
57	
58	```bash
59	export WRKQ_DB_PATH=/Users/lherron/praesidium/var/db/wrkq.db
60	export WRKF_HOOK_CATALOG=/Users/lherron/praesidium/wrkq/.wrkq/wrkf-agent-tasker/hook-catalog.wrapped.json
61	```
62	
63	Most wrkf commands autodiscover the DB from `WRKQ_DB_PATH` and the hook catalog from `WRKF_HOOK_CATALOG` or ancestor `.wrkq/wrkf-*/hook-catalog.wrapped.json`. Set both once at the start of a session.
64	
65	`wrkf next T-XXXXX --role coordinator --json` is your compass — it always tells you what action is next legal, what's blocked, and why. Run it after every transition or evidence add.
66	
67	## Agent roster + role bindings
68	
69	| wrkf role | Agent persona | Best for | Bind via |
70	|---|---|---|---|
71	| `coordinator` | you (clod) | Plan, dispatch, observe, smoke, submit | implicit |
72	| `architect` | cody@workflow-spec or cody@<project> | Plan review, architectural decisions | bind cody@workflow-spec:T-XXXXX |
73	| `red_author` | smokey | Author failing acceptance tests | bind smokey@<project>:T-XXXXX~red |
74	| `implementer` | larry or curly (split work between them) | TypeScript/Bun/Python/Go impl, refactors, bug fixes | bind larry@<project>:T-XXXXX or curly@<project>:T-XXXXX |
75	| `observer` | observer@agent-spaces — dedicated external reviewer, NEVER clod | Final completion review; applies Lance's bypass guardrail | bind observer@agent-spaces:T-XXXXX~observer |
76	| `supervisor` | supervisor@agent-spaces | Recovery / escalation when observer verdict=blocked | bind only when supervisor effect fires |
77	
78	**Per-task scoped handles** (`<agent>@<project>:T-XXXXX`): per-task sessions prevent cross-task confusion. See [[../../../../../.claude/projects/-Users-lherron-praesidium-agent-spaces/memory/feedback_cody_session_reuse.md]] + [[../../../../../.claude/projects/-Users-lherron-praesidium-agent-spaces/memory/feedback_hrcchat_scoped_handles.md]].
79	
80	**Observer must be a different actor from coordinator.** The validator enforces `observer_completion_review.actor != completion_claim.actor`. Use `observer@agent-spaces` (the dedicated agent) — not a seco
…[truncated, 11752 chars]
```
</details>

## 🤖 Assistant · 2026-06-02 10:36

Read it. The wrkf-tasker skill sharpens the whole analysis, because it makes explicit **what wrkf deliberately does *not* enforce** — and that list is almost exactly the CC Workflow conductor's job description.

## What the skill changes about the picture

The skill is candid that wrkf is a **ledger, not a driver**. The coordinator (clod, by hand) carries everything procedural:

> Dispatching the actual hrcchat dm/turn · composing dispatch briefs · choosing surface (`turn --stacked` vs `dm`) · reading replies + on-disk artifacts before recording evidence · sequential vs parallel impl on shared worktree · recovery from stash collisions · refreshing binaries before live smoke · mid-flight polling · `shasum -a 256` artifacts before referencing them.

And on effects:

> the effect handler for `wake_role(red_author)` is **not yet engine-driven** — only `request_observer_review` has a handler shipped.

So today the loop is: `wrkf next` → clod eyeballs it → clod does the real work → clod adds evidence → clod transitions with `--expect-revision`. **That eyeball-and-act loop is precisely a deterministic JS program** — which is what CC Workflow is. The skill even documents the failure mode of *not* having a deterministic driver: incident `wfi_t01489` (self-grading, placeholder `messageId`, missing plan fields caught only on rerun).

## Completing Part 2 — the two execution modes, now grounded

**Mode A — CC subagents play the roles (one turn, ephemeral).** CC `agent()` calls *are* red_author/implementer/observer; validator hooks gate transitions; wrkf records the ledger. Fast, auditable. **But the skill kills this for real work**: it mandates `observer.actor != completion_claim.actor` and "observer must NEVER be clod." One model playing both coordinator and observer violates the core invariant the v3 template added after T-01489. So Mode A is only legitimate for **dry-runs / template testing**, not production coordination.

**Mode B — CC Workflow as conductor over the *real* collective (the right fit).** The script never role-plays; it automates the coordinator's mechanical discipline and dispatches real agents:

```js
// wrkf = truth; CC Workflow = the deterministic coordinator the skill describes by hand
while (true) {
  const n = JSON.parse(await sh(`wrkf next ${task} --role coordinator --json`))
  if (n.kind === 'workflow_complete') break
  switch (n.kind) {                                  // == nextActionModel.rankOrder
    case 'owned_transition_available':
      await sh(`wrkf transition ${task} ${n.transition} --run-checks --expect-revision ${n.rev} --idempotency-key clod-${task}-${n.transition}-${n.rev}`)
      break
    case 'dispatch_ready_worker': {
      await sh(`wrkf run bind ${task} ${n.role} ${n.deliveryRef}`)
      const seq = await sh(`hrcchat turn --stacked 30s ${n.deliveryRef} - <<'EOF'\n${brief(n)}\nEOF`)
      await sh(`wrkf evidence add ${task} --kind dispatch_evidence ...`, {messageId: realSeq(seq)})  // no placeholder
      await sh(`wrkf effect ack ${task} ${n.effectId}`)                                              // close the gap the skill notes
      break
    }
    case 'wait_for_external_owner':
      return {paused:true, on:n}   // a real agent owns the next move — exit, resume on notify
    case 'hard_block_recovery':
      await sh(`wrkf supervisor call ...`); return {escalated:n}
  }
}
```

CC Workflow earns its keep here on exactly the skill's pain points:
- **Determinism over the mechanical steps** — hashing artifacts, `--expect-revision`, idempotency keys, `effect ack`, no placeholder messageIds — encoded once, never forgotten.
- **`pipeline()` for the dispatch_reds/dispatch_impl fan-out** the skill does by hand ("split work between larry and curly"). Each worker becomes a pipeline lane that advances independently; the join is the existing `*_results` `waiting` barrier.
- **The pause/resume contract maps cleanly**: `wait_for_external_owner` → the script returns; when the real agent's evidence lands and `wrkf next` advances, resume via `{scriptPath, resumeFromRunId}` — wrkf's revisioned ledger *is* the durable checkpoint, so CC's cache and wrkf's `--expect-revision` reinforce each other.

The honest boundary: CC Workflow is one background turn. wrkf runs span days and survive restarts. So CC can't *be* the durable run — it can only be **a resumable conductor segment** between human/external waits. That's fine, because wrkf already holds the durable state; CC just drives the next legal burst of mechanical work and exits at the first real wait.

## Net recommendation (highest-value, smallest surface)

Two moves, in order:

1. **Ship a `wrkf`-driven CC Workflow conductor (Mode B) for the coordinator's mechanical loop** — not to replace the wrkf-tasker skill's judgment (brief composition, seam-finding, recovery calls stay human/LLM), but to make the *deterministic* steps deterministic. This directly retires the T-01489 class of bug (placeholder IDs, stale artifacts, forgotten `--expect-revision`).

2. **Then import CC's `parallel`/`pipeline` as a first-class wrkf `map` state** (Part 1, item 1) so the dispatch→join fan-out the skill hand-rolls becomes engine-owned with a real barrier — closing the loop so the conductor's fan-out and the template's join are the same construct.

Everything else from Part 1 (schema-validated evidence, quorum checks, budget obligations, `wrkf plan`) is additive polish on top of those two.

Want me to (a) write a runnable Mode-B conductor script against a throwaway task to prove the loop, or (b) draft the `map`-state template extension as a wrkf-authoring spike? I'd lean (a) first — it's testable today against the live engine without touching wrkf source.

## 🧑 User · 2026-06-02 10:44

You mentioned earlier that workflows run in a sandbox with some primitives/methods injected.  Is that going to limit what we can do in the workflow/wrkf integration?

## 🤖 Assistant · 2026-06-02 10:45

Good catch — and it forces me to correct something. My Mode-B sketch in the last message used `await sh('wrkf next ...')` as if the script could shell out. **It can't.** Let me be precise about the sandbox and what it actually does to the integration.

## The constraint, exactly

The workflow *script* runs in a restricted JS sandbox with only injected primitives. Per the spec:
- **No filesystem, no Node.js API, no shell, (assume) no network** from the script body.
- Banned even within JS: `Date.now()`, `Math.random()`, argless `new Date()` (they break resume).
- The script's *only* channels to the outside world are the injected functions: **`agent()`, `workflow()`, `parallel()`, `pipeline()`, `phase()`, `log()`, `args`, `budget`.**

So the script cannot run `wrkf`, `hrcchat`, `git`, or `shasum`. It cannot read an artifact or parse a CLI's stdout directly. My `sh()` calls were fiction — there is no `sh()`.

## Why it's not a blocker (but reshapes the design)

The side-effects move **inside `agent()`**. A subagent is a full Claude Code instance with Bash/Read/Write/MCP. So the division of labor is:

- **JS script** = deterministic *routing*: the `while` loop, the `switch (next.kind)`, fan-out via `pipeline`, budget guards. Pure compute.
- **`agent()` subagents** = *all I/O*: run `wrkf next --json`, dispatch via `hrcchat`, hash artifacts, add evidence, ack effects, transition. Each returns **schema-validated structured data** the script branches on.

Corrected shape:

```js
const NEXT = { type:'object', required:['kind'], properties:{ kind:{type:'string'}, transition:{type:'string'}, role:{type:'string'}, deliveryRef:{type:'string'}, rev:{type:'integer'}, effectId:{type:'string'} } }

async function wrkfNext(task) {
  return agent(
    `Run: WRKQ_DB_PATH=... WRKF_HOOK_CATALOG=... wrkf next ${task} --role coordinator --json
     Return the parsed JSON object verbatim. Do not act on it.`,
    { schema: NEXT, label:`next:${task}`, phase:'Route' })
}

while (true) {
  const n = await wrkfNext(task)
  if (n.kind === 'workflow_complete') break
  if (n.kind === 'dispatch_ready_worker') {
    // ONE coarse agent does the whole mechanical burst and reports back
    await agent(`Bind ${n.role}=${n.deliveryRef}; hrcchat turn --stacked the brief; capture the REAL messageId
                 (no placeholder); wrkf evidence add dispatch_evidence with it; wrkf effect ack ${n.effectId}.
                 Return {messageId, evidenceId, acked}.`, { schema: DISPATCH_RESULT, phase:'Dispatch' })
  } else if (n.kind === 'owned_transition_available') {
    await agent(`Run wrkf transition ${task} ${n.transition} --run-checks --expect-revision ${n.rev}
                 --idempotency-key clod-${task}-${n.transition}-${n.rev}. Return {ok, newRev}.`, { schema: TRANSITION_RESULT, phase:'Transition' })
  } else if (n.kind === 'wait_for_external_owner') {
    return { paused:true, on:n }   // exit; resume when a real agent advances the ledger
  }
}
```

The deterministic guarantees you wanted **survive** — they live in the JS routing. What you give up is *literal* command execution: each wrkf call is delegated to an LLM that could phrase it wrong. You buy that back with tight prompts + schema-validated returns, and by making steps **coarse** (one agent does dispatch→evidence→ack as a unit) rather than one-agent-per-command (cheaper, fewer seams).

## Three secondary wrinkles, each with a clean answer

1. **No `Date.now()`/`Math.random()` in-script.** The skill needs `lockedAt=now`, `shasum` freshness anchors, idempotency keys. Fine — *those are I/O anyway*, so they belong inside the agent (which can run `date`/`shasum` and read real bytes), not in the script. Idempotency keys are already deterministic (`clod-${task}-${transition}-${rev}`). No conflict.

2. **Resume-cache staleness — the subtle one.** Resume replays cached `agent()` results for the unchanged prefix. But a cached `wrkf next` result is a *snapshot of live ledger state*; replaying it after a real agent advanced the ledger would route on stale data. So a wrkf conductor is a **poor fit for resume-from-cache** on its read steps. The right model: treat each conductor invocation as a **fresh short burst** that re-reads live state, and lean on wrkf's `--expect-revision` + idempotency keys to make re-execution *safe* (a replayed transition just no-ops on revision mismatch). wrkf's revisioned ledger — not CC's cache — is the durable checkpoint. They compose, but you deliberately don't cache the live reads.

3. **Env inheritance.** Whether subagents inherit `WRKQ_DB_PATH`/`WRKF_HOOK_CATALOG` from the session is something I'd *verify* rather than assume; safest is to inline the exports in each agent's command. (This is empirically checkable in a 1-agent probe.)

## One mitigation worth flagging

The weak link is "LLM subagent free-Bashes the wrkf CLI and I trust the parse." You can harden that by exposing **wrkf as an MCP server** (wrkq already ships `mcp-server/`). Then subagents call *typed* `wrkf.next` / `wrkf.transition` tools instead of parsing CLI stdout — the structured contract moves into the tool layer, the agent can't fat-finger flags, and the script's schema just mirrors the tool's output. That turns the delegated I/O from "best-effort shell" into "typed call," which is most of the determinism you'd have wanted from a direct `exec`.

## Bottom line

The sandbox **does not block** the wrkf integration, but it **rules out the script touching wrkf directly** — every wrkf/hrcchat/git/file action must flow through `agent()` and come back as structured data. Net effect: CC Workflow is best cast as the **deterministic router**, with subagents (ideally over a wrkf MCP surface) as the **effectors**, and wrkf's own revisioned ledger — not CC's resume cache — as the durable state. My earlier `sh()` sketch overstated what the script can do; the version above is the real shape. If we build it, the first probe should confirm env inheritance and whether a wrkf MCP tool is the cleaner effector than raw Bash.

## 🧑 User · 2026-06-02 10:53

Would the workflow JS be a one-time build or generated new for every execute?

## 🤖 Assistant · 2026-06-02 10:54

Build-once, and more strongly than you might expect: **one generic conductor for the whole engine**, parameterized per run by `args` — not regenerated per execution.

## Why one-time

The per-task variation you'd think requires a custom script **already lives in the wrkf template + ledger, not in the JS.** The conductor doesn't encode the agent-tasker phases (`diagnosis → planning → … → review`). It encodes the **six `nextActionModel.rankOrder` kinds**:

```
hard_block_recovery > owned_transition_available > missing_evidence_collection
> dispatch_ready_worker > wait_for_external_owner > nonblocking_followup
```

`wrkf next` already collapses the entire state machine into "here's the one next action, its `kind`, `owner`, `command`, `blocksOn`." The conductor is just an **interpreter of that output** — a `switch` over six kinds. That switch is identical for *every* wrkf run and arguably every wrkf *template*, because the `nextActionModel` contract is the stable interface. So it's not even one-per-template; it's potentially one conductor, full stop. New template → same conductor, different `args`.

Note `compactFields` already includes `command` — wrkf often emits the literal command to run. So for most kinds the conductor is nearly a thin executor: take `next.command`, run it via an agent, loop. Only two kinds need LLM judgment (dispatch-brief composition, recovery), and that judgment lives *inside* `agent()` prompts, not in the script structure.

## The three layers

| Layer | Cadence | Contents |
|---|---|---|
| **Conductor JS** | built once, reviewed, saved (`.claude/workflows/wrkf-conduct.js`) | the 6-kind routing loop, fan-out, budget guards — deterministic, never regenerated |
| **`args`** | per execute | `{taskId, project, role→handle bindings, catalogPath, budget}` |
| **`agent()` prompts** | per step, at runtime | per-task judgment: brief content, recovery decisions, evidence composition — the LLM's job, bounded by schema |

Invoke it the way saved workflows are meant to run: `Workflow({ name:'wrkf-conduct', args:{ task:'T-01502', … } })`. The tool persists the script and you re-run by `scriptPath`/name — that reuse model is exactly what saved-workflow + `args` is for.

## When you'd actually touch the JS

Only when the **contract** changes, not when the task changes:
- `nextActionModel.rankOrder` gains/renames a kind (rare — it's the engine's public interface).
- You're authoring a genuinely different orchestration *shape* (e.g. a parallel `map`-state conductor) — that's a new saved workflow, still built once.

## The integrity reason it must be build-once

Regenerating the script per run would reintroduce exactly the variance determinism exists to kill. A fresh-authored conductor each time is an unreviewed program grading its own routing — the T-01489 failure class (placeholder IDs, skipped `--expect-revision`, self-grading) creeping back in through the back door. The whole point of moving the coordinator loop into deterministic JS is that the **routing is fixed, reviewed once, and audited**; the per-run novelty is confined to the wrkf ledger and the bounded `agent()` calls. Build-once isn't just cheaper — it's the property you're buying.

So: **one stable conductor, `args` per run, LLM judgment per step.** The day-to-day "execute" is `Workflow(name, args)` with zero codegen.

## 🧑 User · 2026-06-02 11:38

And we have free rein on the args?  I see us needing to pass some amount of context down to the agents.

## 🤖 Assistant · 2026-06-02 11:39

Yes — free rein on *shape*. `args` is passed **verbatim as any JSON value** and exposed as the global `args` in the script. No schema, no enforced type — object, nested object, arrays, whatever you want. Two hard rules and one design principle.

## The two hard rules

1. **Pass real JSON, not a stringified blob.** `args: {task:"T-01502", bindings:{...}}` — not `args: "{\"task\":...}"`. A stringified value arrives as one string and `args.map`/`args.bindings` throw. (This is the documented footgun.)
2. **`args` is static for the run** — captured at invoke time, doesn't update mid-run. It's also the *escape hatch for the `Date.now()`/`Math.random()` ban*: anything nondeterministic the script needs (a run timestamp, a seed, a freshness anchor) you compute in the main loop and inject via `args`. That's the intended pattern.

## The principle that actually matters for "passing context to agents"

A subtlety that changes the design: **agents do not see `args`.** `args` is visible to the *script* only. A subagent is a fresh context that receives **only what the script puts in its prompt string** (plus inherited env/cwd/tools). So "passing context down" = the script reads `args` and **interpolates the relevant slice into each `agent()` prompt**.

That makes the question "how big should args be?" — and the answer is **pass pointers, not payloads**:

- `args` carries the **control context**: task id, role→handle bindings, catalog path, budget, branch flags, run timestamp. Small, decided values.
- Agents pull the **content context** themselves from the source of truth — `wrkq cat T-XXXXX`, `wrkf task inspect`, `wrkf evidence show`, `Read` the artifact. They have Bash/Read; let them fetch.

Why this and not inlining the spec/artifacts into `args`:
- **Cost**: heavy context inlined into args gets re-embedded into every agent prompt that needs it → tokens multiply across the fan-out. A pointer is a few bytes.
- **Staleness**: wrkf/wrkq is the single source of truth. A copy baked into `args` at invoke time goes stale the moment an agent advances the ledger. Pointers always resolve live.
- **Resume**: `args` must match for cache hits; a big volatile blob makes resume brittle.

## Concrete args shape for the conductor

```js
Workflow({ name:'wrkf-conduct', args: {
  task: 'T-01502',
  project: 'agent-control-plane',
  role: 'coordinator',
  catalog: '/Users/.../wrkf-agent-tasker/hook-catalog.wrapped.json',
  db: '/Users/lherron/praesidium/var/db/wrkq.db',
  bindings: {                                  // control context — small
    architect:   'cody@workflow-spec:T-01502',
    red_author:  'smokey@agent-control-plane:T-01503~red',
    implementer: 'larry@agent-control-plane:T-01504',
    observer:    'observer@agent-spaces:T-01502~observer',
  },
  runStamp: '2026-06-02T14:30:00Z',            // injected because Date.now() is banned
  budgetCeilingTokens: 400000,
  branch: { required: false },
} })
```

Then in the script, each agent prompt weaves only its slice and tells the agent where to pull the rest:

```js
agent(`You are red_author dispatch for ${args.task}.
  Read the task: wrkq cat ${args.task}. Read the plan: wrkf evidence show ${args.task} --kind decomposition_plan.
  Dispatch to ${args.bindings.red_author} via hrcchat turn --stacked; capture the real messageId;
  add dispatch_evidence; ack the wake_role effect. Return {messageId, evidenceId, acked}.`,
  { schema: DISPATCH_RESULT, phase:'Dispatch' })
```

So: **free rein on the structure, keep it to pointers + decided control values, let agents fetch content from wrkf/wrkq.** That keeps args small, resume stable, the ledger authoritative, and the per-fan-out token cost flat.
