# 9. Monitor and webhooks

There are two ways to watch the ledger from outside. `wrkq monitor` streams events and blocks until a
condition holds; it is the agent observation feed for the Claude Monitor tool. **Webhooks** make the daemon
POST to subscribed URLs when tasks, workflows or containers change. Webhooks can be per container (inherited
downward) or global, on the internal root. Code: `internal/rpccli/monitor.go`, `internal/rpccli/webhook.go`,
`internal/rpccli/container.go` (`container set --webhook-*`), `internal/webhooks/` and `internal/webhooksub/`.

## Sub-features

- **monitor wait.** `wrkq monitor wait <TASK|EN...> --until state=<s>|all-terminal|terminal --timeout D
  [--stall-after D]` prints one `wrkq.monitor.terminal` line. Exit codes: 0 when the condition is met, 1 when
  the timeout or stall leaves it unmet, 2 for a selector error, 3 for a stream error. Since 7f26a88 `wait` and
  `watch` take a commit token before their first read and park on `wrkq.commit.wait` (feature 1) between reads,
  for the shortest of 25 s, the time left to `--timeout` and to `--stall-after`, instead of polling: a `wait`
  started 2 s before the `set` returned 2.1 s after start. Stall (2.0 s) and timeout exits are unchanged. A wait
  whose remaining slack is under 1 s logs a false `-32603` in wrkqd's request log (feature 1 Gotchas, T-12764).
  Driven 2026-11-09, T-12762.
- **monitor watch.** `wrkq monitor watch [TASK...] [--state-only] [--until ...] [--raw] [--since <event id>]
  [--timeout D]` streams events. `--raw` gives event-log rows (`id`, `event_type`, `resource_id`, `principal_ref`,
  `scope_ref`, `payload` as a JSON string, so use `fromjson`). Without `--since` it **replays the whole log from
  event 1**, and it ignores task selectors (`monitor watch T-99999 --raw` still streams every event). Every watch
  ends with a `{"type":"wrkq.monitor.terminal",...}` row. A watch without `--until` that runs out of time exits 0
  (`result: timeout`). `--stall-after D` ends a wait or a watch after D with no new events, with or without
  `--until`: `monitor wait` reports `result: stall` and exits 1 (`Error: monitor wait ended: stall`), a watch without
  `--until` reports `result: stall` and exits 0 (driven 2026-10-20, T-11374: both ended at 2s under `--timeout 30s`). Refusals, all exit 2: `--until` with no selector, `monitor wait` without `--until`, and
  `--scope` (`not implemented yet`).
- **`wrkq watch` is gone.** It now fails, naming `wrkq monitor watch --raw`.
- **Tail identity.** The RPC `wrkq.history.tailView {cursor, limit}` (what tailers such as HRC's kicker read)
  returns `{items, high_water, incarnation, head}` since 495d0d1: `incarnation` is the store's
  `collaboration_ledger_meta` id and `head` is `MAX(event_log.id)`, both read in the same snapshot as `items`. A
  cursor above `head` keeps it as `high_water` (cursor 999999 -> `high_water 999999`, `head 125`). A client can tell a
  restored or replaced store from a quiet one. Driven 2026-11-02, T-12309 against `sqlite3 -readonly` on the scratch file.
- **Container webhooks.** `wrkq container set <c> --webhook-url URL [--webhook-events task|workflow|container|<name>]`,
  plus `--add-webhook-url`, `--remove-webhook-url`, `--webhook-urls '<json>'` and `--all`. `{ticket_id}` in a URL
  expands to the task id and `{project_id}` to the subscribing container's `P-<n>`. The body carries `event`,
  `ticket_id`, `event_seq` and more. Every POST carries `User-Agent: wrkq/<version>` (0655f69; all 15 sink rows
  read `wrkq/v0.1.0-555-g401c525`, driven 2026-11-09, T-12762). A comment on a task posts `comment_added` to the `task` class.
  An `ext put` writes a `task.ext.put` row to the raw feed and POSTs nothing, and a replayed `task.update`
  idempotency key POSTs one `updated`, not two (driven 2026-11-09, T-12762).
  `--webhook-events` without a URL is refused. `--webhook-url` replaces the container's whole list, and
  `--add-webhook-url` appends. Without `--webhook-events` the result lists plain URL strings, with it
  `{url, events}` objects (driven 2026-10-20, T-11374).
- **Global webhooks.** `wrkq webhook list|add|rm <url>` manage subscriptions on the internal root, which every
  project inherits. A second `add` of the same URL returns `changed: false`, and a bad URL is refused
  (`invalid webhook url`). `list --json` (hidden, feature 2) and `list --output json` both print one
  `{"url": ...}` object per line, and an empty list prints nothing (2026-10-20, T-11374).

## How to get to it

Run `eval "$(wv env <name>)"`. For webhooks, start a throwaway HTTP sink on a free loopback port that appends
each POST to a file under `$WV_STATE` as `{path, user_agent, body}`. The 10-line Python sink is `~/praesidium/wrkq/spaces/verify-wrkq/fixtures/sink.py`. Subscribe a scratch
container to it.

Live, read only: `wrkq webhook list --output json` shows the global sinks: ACP on loopback and on the tailnet,
and taskboard (three on 2026-10-05). Never add or remove one on
the canonical daemon.

## Driving it

```bash
W=$(~/.local/bin/wrkq touch inbox/wv-watch -t "WV monitor target" --json | jq -r '.[0].id')        # id depends on how many drives ran before
( ~/.local/bin/wrkq monitor wait $W --until state=completed --timeout 30s; echo "exit=$?" ) & sleep 2
~/.local/bin/wrkq set $W --state completed; wait                                                  # result met, exit 0
~/.local/bin/wrkq monitor wait $W --until state=cancelled --timeout 3s; echo "exit=$?"      # timeout, exit 1
~/.local/bin/wrkq monitor watch --raw --timeout 3s | jq -c 'select(.id) | {id,event_type}' | tail -3   # not `| head`: SIGPIPE = exit 141
python3 ~/praesidium/wrkq/spaces/verify-wrkq/fixtures/sink.py <port> "$WV_STATE/sink.jsonl" &
~/.local/bin/wrkq container set inbox --webhook-url "http://127.0.0.1:<port>/hook/{ticket_id}" --webhook-events task
~/.local/bin/wrkq touch inbox/wv-hook -t "WV webhook target"; ~/.local/bin/wrkq set inbox/wv-hook --priority 1; sleep 3
jq -c '{path, user_agent, body: (.body|fromjson|{event, ticket_id, event_seq})}' "$WV_STATE/sink.jsonl"
kill %2   # stop the sink before wv down
```

## Gotchas

- `monitor wait` exits 1 on a timeout and also prints `Error: monitor wait ended: timeout` on stderr. That is
  the documented unmet-condition exit, not a crash.
- A scratch store starts with no webhooks. A **copy** of the canonical store carries the canonical
  `webhook_urls` (ACP, taskboard) and would post scratch events to them. Never serve a copy without clearing
  them first (feature 1).
- Delivery is asynchronous. Give the sink a few seconds before you count its lines, and prove "no delivery" with
  a count after a fixed wait.
- The sink is your own process. `wv down` doesn't stop it.

## Proven when

`monitor wait` returns `result: met` and exits 0 within about 0.1 s of the task completing, and returns `result:
timeout` with exit 1 for a state that never arrives. `watch --raw` streams event rows. With the container subscription set, creating
and then updating a task in it POSTs exactly two `task` events (`created`, `updated`) to `/hook/<task id>`, with
increasing `event_seq` (a comment adds a third, `comment_added`).

Driven 2026-11-09 on wv `t-12762` (T-12762 upkeep) with installed 401c525: `var/wrkq-artifacts/T-12762/09-monitor-webhooks/drive.txt`
and `evidence/t-12762/sink.jsonl` (with `user_agent`): the T-12309 replay (with its `tailView` frames), then the
sub-second `-32603` measurement, wake latency, stall, ext and replay webhook drives after the NOTE drift lines. Live read
of the global sinks (three: ACP loopback and tailnet, taskboard): `live/reads.txt`.
