---
id: wrkq/concepts
title: wrkq concepts
kind: guide
authority: descriptive
status: active
visibility: internal
provenance: authored
---

# wrkq concepts

Use `wrkq <command> --help` or `wrkc <command> --help` for command syntax.

## Rooms and obligations

A room keeps the conversation across runtime changes. A task in a campaign
routes new messages to the campaign room. If the task had its own room, its
older history stays there and links to the campaign room. A named subtask
uses its owner's effective room and keeps its own tag so readers can filter
the shared conversation. When addressing a seat by handle, a task-scoped
recipient's work determines the room first, then a task-scoped sender's work.
Two primary seats use a pair room unless they address an existing room.
Completed work stays reachable for grading and follow-up.

Reply using the envelope's exact `replyTo` handle. Two seats of one agent can
owe different replies. A bare name resolves to the most recently presented
reply-required sender of that name, a unique room member, or the room's task
or project scope. Ambiguous membership refuses the send so the original
obligation stays visible.

A reply acknowledges pending and presented mail from that counterparty in
the room. Use `--discharges` to select particular envelopes, including
deferred ones. Deferral survives runtime rotation. A retry promise makes the
envelope visible again in the pending inbox at the scheduled time while
`show` still reports the stored deferred state. A reply with `--note` can
discharge mail without waking the recipient.

Presentation binds the obligation to the receiving runtime. If that runtime
ends without disposing of it, `runtime_terminated` means the sender can issue
a new envelope. An `unconfirmed` failure means the harness received the body
but a turn never confirmed presentation. Check the room before deciding
whether to resend. `ignored` means the runtime ended its reminder turn
without disposing of the obligation.

The sender's failure inbox follows active work. It drops failures attached
to terminal tasks and hides unattached failures after 24 hours (or when their
room goes stale). This window comes from the observed resend history, where
none of the sampled mail was resent after 24 hours. `show` and `log` retain
the full ledger. A wait timeout by itself leaves the envelope unchanged.

## Handoffs

A handoff is an intentional context record an agent leaves for a **later
session of the same agent** (or, addressed with `--to`, a named successor
agent). It is explicitly not a task, not a comment, and
not memory in the LLM-context sense — it's a durable, queryable row scoped to
one agent/project pair.

- **Scope**: v1 handoffs are scoped to
  `agent:<id>:project:<proj>` (compact handle form: `cody@wrkq`). Task/role
  variants of the scope may parse but are normalized to project scope for
  default handoff flows.
- **Statuses**: `pending`, `acknowledged`. Acknowledgement is the only
  retirement mechanism — handoffs do not auto-expire.
- **Creation requires** a non-empty title and body.
- **Successor addressing**: `wrkq handoff create --to <agent>@<project>` files
  the handoff in the successor agent's scope with you recorded as its author,
  so the successor lists and acknowledges it as its own. A task in the handle
  is ignored.
- **Defaults**: `handoff list` and `handoff search` both default to
  `status=pending`, so a session picking up where another left off sees only
  unresolved handoffs unless it asks for more.
- **Search integration**: `wrkq handoff search` runs against the shared
  sidecar search index, and runs `IndexPending` before querying — so a
  just-created handoff is searchable without a manual `wrkq index rebuild`,
  as long as indexing succeeds.

Use handoffs for continuity between an agent's own successive runs, or with
`--to` for a named successor taking over the same work. Use
`wrkc` rooms and addressed envelopes for inter-agent requests and replies.

## Search and the index sidecar

Search candidates come from a **derived, rebuildable sidecar SQLite database**;
canonical rows still supply current state and filtering. The server owns the
sidecar and embedding configuration. With a remote `rpc://` locator, these
live on the daemon host, not on the CLI host. The default sidecar path is:

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
  search (no external dependency). Configure this on the daemon host for remote
  clients. Dense query failures are recorded and lexical results can still be
  returned; this does not guarantee that every index operation will succeed
  when the embedding server is unavailable.

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
| `wrkq diff <A> <B>` | "Which fields differ between these two tasks?" | JSON field changes for non-TTY output; human field comparison for a TTY. Single-task working-copy comparison is unimplemented; the accepted `--unified` flag does not affect output. |
| `wrkq monitor watch --raw` | "Tail the raw event log live." | Whole-log NDJSON tail; `--since` is an event-ID cursor; `--timeout`/`--stall-after` end it with the monitor terminal line. Replaces the retired `wrkq watch`. |
| `wrkq monitor watch [TASK...]` | "Stream typed, filterable events for specific tasks, built for automation." | NDJSON or compact format, `--event-type`, `--state-only`, `--last N` replay, `--until` condition, `--timeout`/`--stall-after`. Emits exactly one terminal line before exit. |
| `wrkq monitor wait [TASK...]` | "Block a script until a condition holds, then exit." | Same condition evaluator and exit-code contract as `monitor watch --until`; no event streaming to stdout — it's a barrier. |

`wrkq monitor` is explicitly designed for agent observation (its own help
text says "via the Claude Monitor tool") — its `--until state=<s>[,<s>...]`
and `--until all-terminal` conditions plus the fixed exit-code contract
(`0`=condition met, `1`=condition unmet at timeout/stall, `2`=selector error,
`3`=stream error)
make it the right primitive for scripted sequencing, e.g.:

```bash
wrkq monitor wait T-00001 --until state=completed --timeout 30m
```

Without `--until`, a follow bounded by `--timeout` or `--stall-after` exits
`0` when that clock ends, with terminal `result: timeout|stall` and
`unmet: []`. The exit-1 timeout rule applies when a requested condition remains
unmet. The accepted `--scope` flag currently errors as unimplemented.
Conditions also include `acked` and `terminal` for envelope observation;
room selectors support bounded follows without `--until`.

`wrkq monitor watch --raw` is the lower-level, closer-to-the-wire primitive: it
tails the literal event log with no task-condition semantics, useful for
debugging or building your own consumer. It replaces `wrkq watch`, which now
only exits 2 and names it.

## Attribution vs. authentication

Every mutation is attributed to a principal ref (`agent:<id>`), resolved with
this precedence: `--principal-ref`/`--as` flag > `WRKQ_PRINCIPAL_REF` env >
validated runtime scope (`AGENT_SCOPE_REF`, then
`ASP_SCOPE_REF`/`ASP_HANDLE`/`ASP_AGENT_ID`+`ASP_PROJECT`)
reduced to `agent:<id>` > `default_principal_ref` config. wrkq validates
principal *syntax* only — it does not authenticate, does not create actor
rows for ordinary writes, and treats `WRKQ_ACTOR`/`WRKQ_ACTOR_ID`/bare slugs/
`system:*` as non-attributing legacy/display-cache inputs. Remote daemon bearer
credentials authenticate transport access and, for node-token credentials,
establish logical node identity; they do not turn caller-supplied principal
attribution into authenticated agent identity. Runtime ASP scope
provenance (the fuller `scope_ref`) is recorded separately from the reduced
principal — passing a full ScopeRef as a principal input keeps only the agent
identity.
