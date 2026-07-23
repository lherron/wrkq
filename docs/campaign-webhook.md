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
use `{campaign_id}`, `{campaign_uuid}`, and `{campaign_path}`.

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
