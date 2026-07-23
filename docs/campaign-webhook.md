# Campaign transition webhook

`container.campaign_state_changed` uses a container-native webhook body. It does
not populate the task webhook's ticket, project, task-state, label, or workflow
fields.

## Payload

Schema: `wrkq.campaign-transition.v1`

```json
{
  "schema_version": "wrkq.campaign-transition.v1",
  "event": "container.campaign_state_changed",
  "event_id": 31931,
  "idempotency_key": 31931,
  "occurred_at": "2026-07-23T18:30:00Z",
  "actor": "agent:cody",
  "principal_ref": "agent:cody",
  "campaign_uuid": "5bd60adb-0ef4-4ca2-bf52-e2ab43b0366c",
  "campaign_id": "P-00412",
  "campaign_path": "wrkq/campaign",
  "old_campaign_state": "active",
  "new_campaign_state": "completed"
}
```

Conversion uses `old_campaign_state: null` and
`new_campaign_state: "active"`. Close uses `active -> completed|cancelled`.
`actor` retains the established webhook actor field but carries the canonical
principal identity; `principal_ref` makes that authority explicit.

`event_id` is the committed `event_log.id`. `idempotency_key` is the same
integer. Consumers must use it to make repeated deliveries safe.

## Targeting and subscriptions

Targets come from the campaign container's inherited `webhook_urls` chain, the
same nearest-container-through-root union used for task webhooks. Duplicated
URLs collapse after subscription filtering and templating.

Structured subscriptions can select:

- `"container"` or `"container.*"` for container events;
- `"task"` or `"task.*"` for task events only;
- `"workflow"` or `"workflow.*"` for workflow events only;
- `"*"` / `"all"`, a bare URL, or an empty event list for all families.

Task-class subscriptions never receive `container.*`. Campaign templates may
use `{campaign_id}`, `{campaign_uuid}`, and `{campaign_path}`. Those template
vars have no value on a task-shaped delivery and pass through literally
(percent-encoded), so keep campaign-templated URLs on their own
container-class subscription rather than mixing them into a catch-all one.

Write them with `wrkq container set`, either as the stored JSON form or by
pairing `--webhook-events` with the URL flags:

```bash
wrkq container set wave-a \
  --webhook-urls '[{"url":"https://hooks.test/campaign/{campaign_id}","events":["container"]}]'

wrkq container set wave-a \
  --add-webhook-url https://hooks.test/tasks --webhook-events task
```

`--webhook-events` narrows every URL passed with `--webhook-url` /
`--add-webhook-url` in the same invocation. Entries are keyed by URL: adding a
URL that is already subscribed re-points its event narrowing instead of
duplicating it, and `--remove-webhook-url` drops an entry whether it was stored
bare or structured.

## Delivery contract

Dispatch starts only after the transaction containing the campaign state and
`event_log` row commits. Delivery is a best-effort hint:

- zero or more deliveries may occur;
- duplicates are possible;
- a refused, timed-out, or non-successful receiver cannot roll back or fail the
  campaign transition;
- v1 has no webhook outbox or durable retry.

The downstream campaign digest consumer must sweep for closed campaigns without
a digest using `wrkq.container.timelineView`. That sweep—not this webhook—is the
delivery guarantee.
