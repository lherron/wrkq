---
id: wrkq/concepts
title: wrkq concepts - handoffs, search, monitoring
kind: guide
authority: descriptive
status: active
visibility: internal
provenance: authored
---

# wrkq concepts: handoffs, search, monitoring

This page explains three cross-cutting wrkq mechanisms that are easy to
confuse with each other or with adjacent systems: handoffs, the search/index
subsystem, and the event-log/monitor/watch/diff family. Command syntax lives
in `/docs/wrkq/cli-reference`.

## Handoffs

A handoff is an intentional context record an agent leaves for a **later
session of the same agent**. It is explicitly not a task, not a comment, and
not memory in the LLM-context sense — it's a durable, queryable row scoped to
one agent/project pair.

- **Scope**: v1 handoffs are scoped to
  `agent:<id>:project:<proj>` (compact handle form: `cody@wrkq`). Task/role
  variants of the scope may parse but are normalized to project scope for
  default handoff flows.
- **Statuses**: `pending`, `acknowledged`. Acknowledgement is the only
  retirement mechanism — handoffs do not auto-expire.
- **Creation requires** a non-empty title and body.
- **Defaults**: `handoff list` and `handoff search` both default to
  `status=pending`, so a session picking up where another left off sees only
  unresolved handoffs unless it asks for more.
- **Search integration**: `wrkq handoff search` runs against the shared
  sidecar search index, and runs `IndexPending` before querying — so a
  just-created handoff is searchable without a manual `wrkq index rebuild`,
  as long as indexing succeeds.

Platform usage note: handoffs are the mechanism platform skills such as
hrcfork and agent-tasker use to pass session continuity between an agent's
own successive runs — they are a hand-back-to-myself primitive, not an
inter-agent message.

## Search and the index sidecar

wrkq search is not queried against the canonical database directly. It is
served from a **derived, rebuildable sidecar SQLite database**:

```text
<canonical-db>.search.sqlite
```

The index stores chunks for three resource types: `task`, `comment`,
`handoff`. Because it is derived, it is safe to `rebuild` or `vacuum` at any
time — the canonical DB is unaffected, and the sidecar is never a source of
truth for task state.

### Lexical vs. dense

- **Lexical**: FTS5, requires the `sqlite_fts5` build tag (included by
  `just build` / `just install`). Without FTS5 the search service falls back
  to a LIKE-style plain lexical table — functional but lower quality.
- **Dense (optional)**: embeddings from an external `llama-server`. Defaults:
  provider `llama-cpp`, base URL `http://127.0.0.1:18480`, model
  `Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M`, dimension `4096`, index batch size
  `8`. Set `WRKQ_SEARCH_DENSE_PROVIDER=none` to force FTS-only indexing and
  search (no external dependency). If the embedding server is unavailable and
  a dense provider is configured, wrkq can fall back to FTS-only rather than
  failing.

### Index lifecycle

```bash
wrkq index status     # pending count, last update, provider info
wrkq index rebuild     # full rebuild from canonical state
wrkq index update      # index pending canonical changes only
wrkq index vacuum
wrkq index pause       # stop background indexing
wrkq index resume
```

`wrkq search --fresh` fails the query outright if the index is stale, instead
of silently returning results that may be missing recent writes. Use
`--explain` to see ranking diagnostics (useful when relevance ordering looks
wrong).

Default filters worth remembering: `search` scopes task/comment results to
`state=open` unless `--state all` is passed; `all` includes every non-deleted
state, including `archived`.

## Event log, watch, monitor, diff, log

wrkq keeps one append-only `event_log` (task/container/comment mutations with
principal_ref, scope_ref, etag, and JSON payload). Several commands read from
or around it, and they answer different questions:

| Command | Question it answers | Shape |
| --- | --- | --- |
| `wrkq log <ref>` | "What happened to this one task/container over time?" | Paginated history, `--patch` for field-level diffs, `--since`/`--until` date filtering. |
| `wrkq diff <A> [B]` | "What's different between these two tasks (or two versions)?" | Unified-diff-style comparison, `--unified N` context lines. |
| `wrkq watch [PATH...]` | "Tail the raw event log live." | Unfiltered (or `--since`-bounded) event stream; `--ndjson`; `--follow` (default true). |
| `wrkq monitor watch [TASK...]` | "Stream typed, filterable events for specific tasks, built for automation." | NDJSON or compact format, `--event-type`, `--scope`, `--state-only`, `--last N` replay, `--until` condition, `--timeout`/`--stall-after`. Emits exactly one terminal line before exit. |
| `wrkq monitor wait [TASK...]` | "Block a script until a condition holds, then exit." | Same condition evaluator and exit-code contract as `monitor watch --until`; no event streaming to stdout — it's a barrier. |

`wrkq monitor` is explicitly designed for agent observation (its own help
text says "via the Claude Monitor tool") — its `--until state=<s>[,<s>...]`
and `--until all-terminal` conditions plus the fixed exit-code contract
(`0`=condition met, `1`=timeout/stall, `2`=selector error, `3`=stream error)
make it the right primitive for scripted sequencing, e.g.:

```bash
wrkq monitor wait T-00001 --until state=completed --timeout 30m
```

`wrkq watch` is the lower-level, closer-to-the-wire primitive: it tails the
literal event log with no task-condition semantics, useful for debugging or
building your own consumer.

## Attribution vs. authentication

Every mutation is attributed to a principal ref (`agent:<id>`), resolved with
this precedence: `--principal-ref`/`--as` flag > `WRKQ_PRINCIPAL_REF` env >
validated ASP scope (`ASP_SCOPE_REF`/`ASP_HANDLE`/`ASP_AGENT_ID`+`ASP_PROJECT`)
reduced to `agent:<id>` > `default_principal_ref` config. wrkq validates
principal *syntax* only — it does not authenticate, does not create actor
rows for ordinary writes, and treats `WRKQ_ACTOR`/`WRKQ_ACTOR_ID`/bare slugs/
`system:*` as non-attributing legacy/display-cache inputs. Runtime ASP scope
provenance (the fuller `scope_ref`) is recorded separately from the reduced
principal — passing a full ScopeRef as a principal input keeps only the agent
identity.
