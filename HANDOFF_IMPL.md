# T-01589 Handoff Feature — Implementation Handoff

## TL;DR for a fresh session

The handoff CLI surface is shipped, tested, installed, and exercised end-to-end against the canonical DB. **What's missing is HTTP exposure (wrkqd has no `/handoffs` routes), unified-search index integration (the indexer lives on `feat-wrkq-search`, not on `main`), and integration with the taskboard/workboard UIs.** The feature works for CLI-driven agent flows today; it is not yet a first-class web/HTTP citizen.

## Where it lives

- **Branch**: `main` (developed directly on main per Lance — no feature branch).
- **Parent task**: T-01589 (state: `draft` — Lance/cody to close).
- **Plan history**: `/tmp/clod-t01589/plan-v2.md` (the consensus plan after cody review + Lance's redirect to canonical agent-scope grammar).
- **Spec doc updates**: `docs/DOMAIN-MODEL.md` (handoff section), `docs/CLI-REFERENCE.md` (verb reference + `wrkq agent-context`).

## Commits landed on main (chronological)

| Commit | Phase | Description |
|---|---|---|
| `8f21e04` | A1 (T-01593) | Schema migration `000015_handoff_schema.sql` + `H-NNNNN` id format + `event_log` CHECK includes `handoff` and `comment` |
| `aebed9f` | A2 (T-01594) | Go port of `agent-scope` grammar into `internal/scope/` + `wrkq agent-context` CLI |
| `a6cf8e5` | B1 (T-01595) | `internal/store/handoffs.go` — Create/Get/List/Acknowledge/Search (LIKE-based) |
| `960b9ef` | B2 (T-01596) | Cobra group + 5 subcommand stubs + `ls`/`cat` aliases |
| `5322e81` | C1 (T-01597) | `wrkq handoff create` — idempotency replay, exit 3 on payload mismatch, self-scope guard, TTY-aware output |
| `53542c6` | C2 (T-01598) | `wrkq handoff list` — default pending, cursor pagination, NDJSON streaming, porcelain |
| `ffbdfdb` | C3 (T-01599) | `wrkq handoff get` — accepts H-id or UUID, exit 4 on not-found |
| `e06d001` | C4 (T-01600) | `wrkq handoff acknowledge` — optional `--note`, `--dry-run`, `--if-match`, exits 5/6 |
| `9a05665` | C5 (T-01601) | `wrkq handoff search` — LIKE over title/body/scope, status filter, pagination |
| `5196f73` | D1 (T-01602) | `DOMAIN-MODEL.md` + `CLI-REFERENCE.md` sections + `--porcelain` help-text polish |
| `050f45f` | D2 (T-01603) | Cobra integration round-trip test + exit-code contract subtests (0/1/2/3/4/5/6) |

All 11 sub-tasks closed by their assignees. Parent T-01589 remains `state: draft` (coordinator does not close umbrella tasks).

## Sibling tasks (related but separate)

- **T-01592** — Remove `WRKQ_ACTOR` / `WRKQ_ACTOR_ID` env vars; use ASP scopeRef agentId. Filed during T-01589 planning per Lance's instruction. **Not started.** Handoffs already sidestep `WRKQ_ACTOR` by deriving `created_by_agent_id` from `ASP_AGENT_ID` via `ASP_SCOPE_REF`; T-01592 generalizes that approach to comments, whoami, doctor, etc.

## What works today (verified end-to-end against canonical DB)

End-of-feature live smoke (T-01589 H-00001 in `/Users/lherron/praesidium/var/db/wrkq.db`):

1. `wrkq agent-context --json` — resolves `ASP_SCOPE_REF=agent:clod:project:wrkq:task:primary` → canonical `agent:clod:project:wrkq` (task dropped per v1 normalize). DB-backed actor + container UUIDs resolved.
2. `wrkq handoff create -t "..." --body-file -` — H-00001 inserted, status=pending, `created_by_agent_id=clod`, FK UUIDs populated.
3. `wrkq handoff list --json` — pending only (default).
4. `wrkq handoff get H-00001 --human` — vertical key/value block + body separator.
5. `wrkq handoff search "agent session" --status all` — LIKE match on body.
6. `wrkq handoff acknowledge H-00001 --note "..."` — flip to acknowledged; event_log gets `handoff.acknowledged`.
7. `wrkq handoff acknowledge H-00001` (repeat) — exit 5, structured `already_acknowledged` error with `acknowledged_at` reference.
8. `wrkq handoff list --status acknowledged` — finds H-00001.

`event_log` audit rows for H-00001:

```
handoff | handoff.created      | etag=1 | 2026-05-22T06:19:33Z
handoff | handoff.acknowledged | etag=2 | 2026-05-22T06:19:42Z
```

## What is NOT done (continuation candidates)

### 1. No HTTP exposure (wrkqd has no handoff routes)

`grep handoff` across `cmd/wrkqd/`, `internal/webhooks/`, `internal/cli/server.go`, `internal/cli/daemon.go` returns zero matches. The wrkqd HTTP server has no `/handoffs/*` endpoints. CLI agents go directly to SQLite, which is fine for agent-driven flows but blocks:
- Taskboard/workboard UI surfacing handoffs
- Cross-host handoff exchange via HTTP
- Any non-Go consumer

**Suggested follow-up task**: `T-XXXXX Add wrkqd HTTP endpoints for /handoffs` — mirror the comment/task endpoint pattern, JSON in/out, scope resolution server-side (via the same `internal/scope` package).

### 2. No unified search-index integration

`internal/search/` does not exist on `main`. The FTS5 + dense-vector search infrastructure lives only on the `feat-wrkq-search` branch (commit `bdac424`). Per cody's Q3 sign-off, v1 handoff search uses dedicated SQL LIKE over the `handoffs` table — this works against canonical and is verified, but means handoffs do NOT appear in any unified `wrkq search` results.

When `feat-wrkq-search` merges to `main`:

- Add a `handoff` resource type to `internal/search/render/render.go` (sibling to `renderTaskChunk`, `renderCommentChunk`).
- The `search_chunks` schema currently asserts `task_uuid NOT NULL` (cody flagged this as the hidden coupling that ruled out unified search for v1). It needs a generic resource ref or a nullable `task_uuid` before handoffs can ride the same index.
- Add a `handoff` case to the indexer's dirty-tracking and the `AllSearchable*` enumeration.

**Suggested follow-up task**: `T-XXXXX Extend unified wrkq search to include handoffs (post feat-wrkq-search merge)` — gated on the search-infra branch landing.

### 3. wrkqd lifecycle / stackctl integration

- `stackctl restart dev` does NOT start wrkqd. `stackctl` knows how to CHECK wrkqd's status but has no start verb for it. The Tuesday-spawned wrkqd tmux session died at some point this week.
- I manually started wrkqd at HEAD on `127.0.0.1:7171` during this session via:
  ```
  tmux new-session -d -s stack-dev-taskboard-wrkqd \
    "wrkqd --addr 127.0.0.1:7171 --token dev \
       --db /Users/lherron/praesidium/var/db/wrkq.db \
       2>&1 | tee -a /Users/lherron/praesidium/var/logs/control-plane/taskboard-wrkqd.log"
  ```
  `curl http://127.0.0.1:7171/admin/status` returns 404 (daemon answered, route does not exist on wrkqd's mux). `stackctl status dev` still reports `❌ wrkq-server (no response)` because the health probe at `~/praesidium/stackctl/bin/stackctl:578` is looking for an endpoint that doesn't match wrkqd's actual routes.
- This pre-dates T-01589 — it's a stackctl ↔ wrkqd integration drift, not anything we introduced. But it is in the loop because Lance asked about wrkqd state.

**Suggested follow-up task**: `T-XXXXX Reconcile stackctl wrkqd lifecycle` — either teach stackctl to start wrkqd (mirroring taskboard-api lifecycle) or change the health probe to hit a route wrkqd actually serves. Likely lands cleanly with the search-server lifecycle work on `feat-wrkq-search` (commit `bdac424`'s message: "feat: add search indexing and **wrkq server lifecycle**").

### 4. No `/handoff` skill yet (T-01589 AC12)

T-01589 acceptance criterion 12: "A follow-up `/handoff` skill can be written against stable CLI behavior." That skill is out of scope for the wrkq repo; it lives under `~/praesidium/var/agents/<agent>/skills/` or in `metaskills/`. The CLI is now stable enough to author the skill.

**Suggested follow-up task** (in metaskills or per-agent home): `Author /handoff skill — load pending handoffs at session start; create handoff before session end; acknowledge consumed handoffs.`

### 5. Polish / minor

- `wrkq agent-context --json` does NOT expose diagnostics in some output paths (curly noted in their A2 DM that diagnostics surface in stderr human mode and `diagnostics: [...]` in JSON, but the live `agent-context` JSON I ran does not show a `diagnostics` key when scope is clean — verify whether the field is omitted when empty or genuinely missing).
- `wrkq handoff create` exits 2 on unresolvable scope, but the error structure differs slightly between the unresolved-env case and the self-scope-violation case — confirm they are both shaped per spec.
- `wrkq handoff list` clamps `--limit` to 500 with a warning to stderr; verify the warning copy is clear when called from automation.

## Installed binary state

After `just install` at HEAD (`050f45f`):

```
~/.local/bin/wrkq      (15.7 MB, includes all 5 handoff verbs + agent-context)
~/.local/bin/wrkf      (9.4 MB, unchanged by T-01589)
~/.local/bin/wrkqadm   (15.7 MB, includes 000015 migration)
~/.local/bin/wrkqd     (16.2 MB, no handoff routes — see §1)
```

Canonical DB at `/Users/lherron/praesidium/var/db/wrkq.db` has migration 000015 applied (verified by inspecting `.tables` and `SELECT FROM handoffs`).

## Decisions captured (so a fresh session knows the why)

- **Branch**: develop directly on `main`. Lance overrode cody's recommendation for a feature branch.
- **Scope grammar**: use canonical `agent:agentId:project:projectId[:task:taskId][:role:roleName]` per `~/praesidium/agent-spaces/packages/agent-scope/`. v1 normalizes down to `kind: 'project'`. `scope_kind` column accepts the full grammar in CHECK so future task/role variants don't require a migration.
- **Actor identity**: `created_by_agent_id` is derived from the parsed scope (which itself comes from `ASP_AGENT_ID` / `ASP_SCOPE_REF`). No dependency on `WRKQ_ACTOR` (which is slated for removal in T-01592).
- **Search**: dedicated LIKE over the `handoffs` table for v1 (cody's Q3 ruling). Unified-search integration deferred until `feat-wrkq-search` merges.
- **Self-scope guard**: `--scope` on `handoff create` must match runtime agent when known (`scope.EnforceSelfScope`). v1 design choice; can be relaxed if cross-agent handoff writes become a use case.
- **`--note` on acknowledge**: optional (cody amendment over the spec's required-note phrasing). Must be non-empty if provided.
- **Soft-delete**: not added. v1 is strictly `pending → acknowledged`.
- **Idempotency**: keyed on `(scope_ref, idempotency_key)`. Replay returns existing handoff + `idempotent_replay: true`. Same key with materially different title/body → exit 3 + structured `idempotency_payload_mismatch`.
- **Exit code contract** (verified by D2 integration test):
  - 0 success / idempotent replay
  - 1 validation
  - 2 unresolvable scope
  - 3 idempotency payload mismatch
  - 4 not found
  - 5 already acknowledged
  - 6 etag mismatch

## How to continue (suggested next steps for a fresh coordinator session)

1. **File the three follow-up tasks** (§§1, 2, 3 above) so they don't get lost — even if Lance defers them.
2. **Close T-01589** if Lance/cody is satisfied with the CLI-only delivery (or leave open if HTTP/search/UI integration is part of the same umbrella).
3. **Start T-01592** (WRKQ_ACTOR removal) — this is the natural next thing because it generalizes T-01589's scope-driven identity approach.
4. **Write the `/handoff` skill** (AC12) once T-01589 is closed — straightforward against the stable CLI surface.

## Reference

- Parent task: `wrkq cat T-01589`
- Plan: `/tmp/clod-t01589/plan-v2.md`
- Subordinate impl tasks (all `state: completed`): T-01593, T-01594, T-01595, T-01596, T-01597, T-01598, T-01599, T-01600, T-01601, T-01602, T-01603
- Follow-up filed: T-01592 (WRKQ_ACTOR removal)
- Agent-scope canonical source (TypeScript): `~/praesidium/agent-spaces/packages/agent-scope/src/`
- Agent-scope Go port (this repo): `internal/scope/`
- Coordinator memory updated: `~/.claude/projects/-Users-lherron-praesidium-wrkq/memory/feedback_no_unauthorized_install.md` — `just install` + e2e smoke is canonical validation per Lance's CLAUDE.md; do not restrict for sequential impl agents.
