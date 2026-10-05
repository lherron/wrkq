# 9. Monitor and webhooks

There are two ways to watch the ledger from outside. `wrkq monitor` streams events and blocks until a
condition holds; it is the agent observation feed for the Claude Monitor tool. **Webhooks** make the daemon
POST to subscribed URLs when tasks, workflows or containers change. Webhooks can be per container (inherited
downward) or global, on the internal root. Code: `internal/rpccli/monitor.go`, `internal/rpccli/webhook.go`,
`internal/rpccli/container.go` (`container set --webhook-*`), `internal/webhooks/` and `internal/webhooksub/`.

## Sub-features

- **monitor wait.** `wrkq monitor wait <TASK|EN...> --until state=<s>|all-terminal|terminal --timeout D
  [--stall-after D]` prints one `wrkq.monitor.terminal` line. Exit codes: 0 when the condition is met, 1 when
  the timeout or stall leaves it unmet, 2 for a selector error, 3 for a stream error.
- **monitor watch.** `wrkq monitor watch [TASK...] [--state-only] [--until ...] [--raw] [--since <event id>]
  [--timeout D]` streams events. `--raw` gives event-log rows (`id`, `event_type`, `resource_id`, `principal_ref`,
  `scope_ref`, `payload`).
- **`wrkq watch` is gone.** It now fails, naming `wrkq monitor watch --raw`.
- **Container webhooks.** `wrkq container set <c> --webhook-url URL [--webhook-events task|workflow|container|<name>]`,
  plus `--add-webhook-url`, `--remove-webhook-url`, `--webhook-urls '<json>'` and `--all`. `{ticket_id}` in a URL
  expands to the task id. The body carries `event`, `ticket_id`, `event_seq` and more.
- **Global webhooks.** `wrkq webhook list|add|rm <url>` manage subscriptions on the internal root, which every
  project inherits.

## How to get to it

Run `eval "$(wv env <name>)"`. For webhooks, start a throwaway HTTP sink on a free loopback port that appends
each POST to a file under `$WV_STATE`. The 10-line Python sink is `09-monitor-webhooks/sink.py` in T-10298's
evidence dir. Subscribe a scratch
container to it.

Live, read only: `wrkq webhook list` shows the global sinks (ACP and taskboard). Never add or remove one on
the canonical daemon.

## Driving it

```bash
wrkq touch inbox/wv-watch -t "WV monitor target"                                   # T-00006 on a fresh drive
( wrkq monitor wait T-00006 --until state=completed --timeout 30s; echo "exit=$?" ) & sleep 2
wrkq set T-00006 --state completed; wait                                            # result met, exit 0
wrkq monitor wait T-00006 --until state=cancelled --timeout 3s; echo "exit=$?"      # timeout, exit 1
wrkq monitor watch --raw --timeout 3s | head -5
python3 <evidence>/09-monitor-webhooks/sink.py <port> "$WV_STATE/sink.jsonl" &          # var/wrkq-artifacts/T-10298/...
wrkq container set inbox --webhook-url "http://127.0.0.1:<port>/hook/{ticket_id}" --webhook-events task
wrkq touch inbox/wv-hook -t "WV webhook target"; wrkq set inbox/wv-hook --priority 1; sleep 3
jq -c '{path, body: (.body|fromjson|{event, ticket_id, event_seq})}' "$WV_STATE/sink.jsonl"
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

`monitor wait` returns `result: met` and exits 0 once the task completes, and returns `result: timeout` with exit 1
for a state that never arrives. `watch --raw` streams event rows. With the container subscription set, creating
and then updating a task in it POSTs exactly two `task` events (`created`, `updated`) to `/hook/<task id>`, with
increasing `event_seq`.

Driven 2026-10-05 on wv `t-10298` (T-10298): `var/wrkq-artifacts/T-10298/09-monitor-webhooks/drive.txt`. Live
read of the global sinks (hosts only): `live/canonical.txt`.
