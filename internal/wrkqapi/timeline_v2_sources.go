//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The v2 reader's two sources: the raw scans of event_log and project_events
// over an id window, and the SQL predicates and joins they share.

type timelineQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type timelineRawEvent struct {
	entry       WrkqTimelineEntry
	eventType   string
	payload     string
	serverTime  string
	commentID   string
	commentKind sql.NullString
	commentBody string
	commentMeta sql.NullString
	envelope    timelineRawEnvelope
	// subtask is true when the event's task row has a subtask owner.
	subtask bool
}

// timelineRawEnvelope carries the room-message columns for an envelope.created
// row. Unlike a comment, an envelope event's payload holds no container or
// campaign uuid, so affiliation is resolved in SQL through the ROOM: a task
// room affiliates exactly as a comment on that task would, and a container room
// affiliates to its own container.
type timelineRawEnvelope struct {
	id          sql.NullString
	groupID     sql.NullString
	roomID      sql.NullString
	roomKind    sql.NullString
	from        sql.NullString
	obligation  sql.NullString
	body        sql.NullString
	container   sql.NullString
	campaign    sql.NullString
	fromProject sql.NullString
	toProjects  sql.NullString
}

type timelineRawProjectEvent struct {
	entry      WrkqTimelineEntry
	id         int64
	semantic   string
	serverTime string
}

// timelineSupportedEventTypes are the stored event types the reader delivers,
// each subject to affiliation; every other raw row only advances the cursor.
var timelineSupportedEventTypes = []string{
	"comment.created", "envelope.created", "task.outcome_set",
	"task.archived", "task.deleted", "task.restored", "task.purged",
	"container.campaign_state_changed", "task.created", "task.updated",
	"task.moved", "task.claimed", "task.claim_released",
}

func loadTimelineRawEvents(ctx context.Context, tx timelineQueryer, low, high int64, desc bool, types []string, since, before *time.Time, selectedTasks, affiliation map[string]bool, root string) ([]timelineRawEvent, bool, error) {
	// The envelope joins are guarded on event_type, so a non-envelope row costs
	// one NULL probe. The `env` join carries the fan-out collapse in its own ON
	// clause: a say to N addressees writes N rows sharing one group_id whose
	// value is the FIRST envelope's own id, so exactly one row per say satisfies
	// id = group_id. The other N-1 join to nothing and are dropped by the same
	// delivery filter that drops an unsupported event type. COALESCE guards a
	// null group_id so an unstamped row reports itself rather than vanishing.
	predicate, extra := timelineSourcePredicate("e.event_type", "e.timestamp", types, since, before)
	query := timelineOrdered(`
		SELECT e.id, e.timestamp, COALESCE(e.principal_ref, ''), COALESCE(e.scope_ref, ''), COALESCE(e.resource_uuid, ''),
		       e.event_type, COALESCE(e.payload, ''),
		       COALESCE(t.uuid, comment_task.uuid, env_task.uuid, ''),
		       COALESCE(t.id, comment_task.id, env_task.id, ''),
		       COALESCE(tp.path, comment_tp.path, env_tp.path, json_extract(e.payload, '$.slug'), ''),
		       COALESCE(cm.id, ''), cm.kind, COALESCE(cm.body, ''), cm.meta,
		       env.id, COALESCE(env.group_id, env.id), rm.id, rm.kind,
		       COALESCE(env.from_scope_ref, env.from_principal_ref),
		       env.obligation, env.body,
		       COALESCE(rm.container_uuid, room_task.project_uuid),
		       room_task.campaign_uuid,
		       env.from_project_uuid,
		       (SELECT group_concat(to_project_uuid) FROM envelopes sibling
		         WHERE COALESCE(sibling.group_id, sibling.id) = COALESCE(env.group_id, env.id)
		           AND sibling.to_project_uuid IS NOT NULL),
		       t.subtask_owner_uuid IS NOT NULL
		  FROM event_log e
		  LEFT JOIN tasks t ON e.resource_type = 'task' AND t.uuid = e.resource_uuid
		  LEFT JOIN v_task_paths tp ON tp.uuid = t.uuid
		  LEFT JOIN comments cm ON e.event_type = 'comment.created' AND cm.uuid = e.resource_uuid
		  LEFT JOIN tasks comment_task ON e.event_type = 'comment.created' AND comment_task.uuid = json_extract(e.payload, '$.task_id')
		  LEFT JOIN v_task_paths comment_tp ON comment_tp.uuid = comment_task.uuid
		  LEFT JOIN envelopes env ON e.event_type = 'envelope.created' AND env.uuid = e.resource_uuid
		                         AND env.id = COALESCE(env.group_id, env.id)
		  LEFT JOIN rooms rm ON rm.uuid = env.room_uuid
		  LEFT JOIN tasks room_task ON room_task.uuid = rm.task_uuid
		  LEFT JOIN tasks env_task ON env_task.uuid = COALESCE(env.task_uuid, rm.task_uuid)
		  LEFT JOIN v_task_paths env_tp ON env_tp.uuid = env_task.uuid
		 WHERE e.id > ? AND e.id <= ?
		 ORDER BY e.id %s LIMIT ?`, desc)
	if len(selectedTasks) == 0 && len(types) > 0 {
		query = strings.Replace(query, "FROM event_log e", "FROM event_log e INDEXED BY event_log_type_time_idx", 1)
	}

	// Resource identities let SQLite seek event_log_resource_idx for each task,
	// comment and envelope rather than hydrate every global event since the floor.
	// Comments remain addressable through their immutable payload after deletion.
	if len(selectedTasks) > 0 {
		tasks, taskArgs := timelineSelectionList(selectedTasks)
		predicate += ` AND e.id IN (
   SELECT id FROM event_log INDEXED BY event_log_resource_idx
    WHERE resource_type = 'task' AND resource_uuid IN (` + tasks + `)
   UNION ALL
   SELECT ev.id FROM comments c CROSS JOIN event_log ev INDEXED BY event_log_resource_idx
    ON ev.resource_type = 'comment' AND ev.resource_uuid = c.uuid
    WHERE c.task_uuid IN (` + tasks + `)
   UNION ALL
   SELECT ev.id FROM envelopes en CROSS JOIN event_log ev INDEXED BY event_log_resource_idx
    ON ev.resource_type = 'envelope' AND ev.resource_uuid = en.uuid
    WHERE en.task_uuid IN (` + tasks + `)
   UNION ALL
   SELECT ev.id FROM rooms r CROSS JOIN envelopes en ON en.room_uuid = r.uuid
    CROSS JOIN event_log ev INDEXED BY event_log_resource_idx
    ON ev.resource_type = 'envelope' AND ev.resource_uuid = en.uuid
    WHERE r.task_uuid IN (` + tasks + `)
   UNION ALL
   SELECT ev.id FROM event_log ev INDEXED BY event_log_type_time_idx
    WHERE ev.event_type = 'comment.created'
     AND json_extract(ev.payload, '$.task_id') IN (` + tasks + `)
     AND NOT EXISTS (SELECT 1 FROM comments c WHERE c.uuid = ev.resource_uuid)
  )`
		for i := 0; i < 5; i++ {
			extra = append(extra, taskArgs...)
		}
	}
	// Unsupported raw events retain their capped cursor advancement.
	// Moves affiliate to both immutable endpoints. Envelope affiliation comes
	// from the owned room or stamped project endpoints, including all siblings.
	affiliated, affiliationArgs := timelineSelectionList(affiliation)
	predicate += ` AND (
  e.event_type NOT IN ('` + strings.Join(timelineSupportedEventTypes, "', '") + `')
  OR json_extract(e.payload, '$.campaign_uuid') = ?
  OR json_extract(e.payload, '$.container_uuid') IN (` + affiliated + `)
  OR (e.event_type = 'task.moved' AND (
   json_extract(e.payload, '$.oldContainerUuid') IN (` + affiliated + `)
   OR json_extract(e.payload, '$.newContainerUuid') IN (` + affiliated + `)))
  OR (e.event_type = 'envelope.created' AND (
   COALESCE(rm.container_uuid, room_task.project_uuid) IN (` + affiliated + `)
   OR room_task.campaign_uuid = ?
   OR (rm.kind IN ('adhoc', 'project') AND (
    env.from_project_uuid = ? OR EXISTS (
     SELECT 1 FROM envelopes sibling
      WHERE (sibling.group_id = COALESCE(env.group_id, env.id)
       OR (sibling.group_id IS NULL AND sibling.id = COALESCE(env.group_id, env.id)))
       AND sibling.to_project_uuid = ?)))))
 )`
	extra = append(extra, root)
	for i := 0; i < 4; i++ {
		extra = append(extra, affiliationArgs...)
	}
	extra = append(extra, root, root, root)

	query = strings.Replace(query, " ORDER BY e.id", predicate+" ORDER BY e.id", 1)
	args := append([]any{low, high}, extra...)
	args = append(args, monitorMaxPageLimit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	result := []timelineRawEvent{}
	for rows.Next() {
		var raw timelineRawEvent
		if err := rows.Scan(
			&raw.entry.EventID, &raw.serverTime, &raw.entry.PrincipalRef, &raw.entry.ScopeRef, &raw.entry.ResourceUUID,
			&raw.eventType, &raw.payload, &raw.entry.TaskUUID, &raw.entry.TaskID, &raw.entry.TaskPath,
			&raw.commentID, &raw.commentKind, &raw.commentBody, &raw.commentMeta,
			&raw.envelope.id, &raw.envelope.groupID, &raw.envelope.roomID, &raw.envelope.roomKind,
			&raw.envelope.from, &raw.envelope.obligation, &raw.envelope.body,
			&raw.envelope.container, &raw.envelope.campaign,
			&raw.envelope.fromProject, &raw.envelope.toProjects,
			&raw.subtask,
		); err != nil {
			return nil, false, NewInternalError(err)
		}
		raw.entry.Timestamp = toRFC3339(raw.serverTime)
		result = append(result, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, false, NewInternalError(err)
	}
	return result, len(result) == monitorMaxPageLimit, nil
}

func loadTimelineRawProjectEvents(ctx context.Context, tx timelineQueryer, low, high int64, desc bool, filters []string, since, before *time.Time, selectedTasks, affiliation map[string]bool, root string) ([]timelineRawProjectEvent, bool, error) {
	predicate, extra := timelineProjectPredicate(filters, since, before)
	query := timelineOrdered(timelineProjectEventsRawQuery, desc)
	if len(selectedTasks) == 0 && len(filters) > 0 {
		query = strings.Replace(query, "FROM project_events pe", "FROM project_events pe INDEXED BY project_events_type_time_idx", 1)
	}

	if len(selectedTasks) > 0 {
		tasks, args := timelineSelectionList(selectedTasks)
		predicate += " AND pe.task_uuid IN (" + tasks + ")"
		extra = append(extra, args...)
		query = strings.Replace(query, "FROM project_events pe", "FROM project_events pe INDEXED BY project_events_task_idx", 1)
	}
	affiliated, affiliationArgs := timelineSelectionList(affiliation)
	predicate += " AND (pe.container_uuid IN (" + affiliated + ") OR pe.campaign_uuid = ?)"
	extra = append(extra, affiliationArgs...)
	extra = append(extra, root)
	query = strings.Replace(query, " ORDER BY pe.id", predicate+" ORDER BY pe.id", 1)
	args := append([]any{low, high}, extra...)
	args = append(args, monitorMaxPageLimit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	result := []timelineRawProjectEvent{}
	for rows.Next() {
		var raw timelineRawProjectEvent
		var principal, scope, campaign, task sql.NullString
		var detail WrkqTimelineProjectEvent
		var attributes string
		if err := rows.Scan(
			&raw.id, &detail.UUID, &raw.semantic, &attributes,
			&principal, &scope, &detail.Summary, &detail.OccurredAt,
			&raw.serverTime, &raw.entry.ContainerUUID, &campaign, &task,
			&raw.entry.TaskID, &raw.entry.TaskPath,
		); err != nil {
			return nil, false, NewInternalError(err)
		}
		detail.Attributes = json.RawMessage(attributes)
		detail.Type = raw.semantic
		detail.PrincipalRef = nullStringPtr(principal)
		detail.ScopeRef = nullStringPtr(scope)
		detail.OccurredAt = toRFC3339(detail.OccurredAt)
		raw.entry.Type = "project.event"
		raw.entry.Timestamp = toRFC3339(raw.serverTime)
		if detail.PrincipalRef != nil {
			raw.entry.PrincipalRef = *detail.PrincipalRef
		}
		if detail.ScopeRef != nil {
			raw.entry.ScopeRef = *detail.ScopeRef
		}
		raw.entry.CampaignUUID = nullStringPtr(campaign)
		if task.Valid {
			raw.entry.TaskUUID = task.String
		}
		raw.entry.ProjectEvent = &detail
		result = append(result, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, false, NewInternalError(err)
	}
	return result, len(result) == monitorMaxPageLimit, nil
}

// project_uuid is intentionally absent: current subtree membership is the one
// project reader authority; the stored project is idempotency scope only.
const timelineProjectEventsRawQuery = `
		SELECT pe.id, pe.uuid, pe.type, pe.attributes, pe.principal_ref, pe.scope_ref,
		       pe.summary, pe.occurred_at, pe.created_at,
		       pe.container_uuid, pe.campaign_uuid, pe.task_uuid,
		       COALESCE(t.id, ''), COALESCE(tp.path, '')
		  FROM project_events pe
		  LEFT JOIN tasks t ON t.uuid = pe.task_uuid
		  LEFT JOIN v_task_paths tp ON tp.uuid = t.uuid
		 WHERE pe.id > ? AND pe.id <= ?
		 ORDER BY pe.id %s LIMIT ?`

// timelineOrdered stamps the scan direction into a source query. The token is
// from a closed set, never caller text.
func timelineOrdered(query string, desc bool) string {
	if desc {
		return fmt.Sprintf(query, "DESC")
	}
	return fmt.Sprintf(query, "ASC")
}

func timelineSourcePredicate(typeColumn, timeColumn string, types []string, since, before *time.Time) (string, []any) {
	parts := []string{}
	args := []any{}
	if len(types) > 0 {
		if len(types) == 1 {
			parts = append(parts, "AND "+typeColumn+" = ?")
		} else {
			parts = append(parts, "AND "+typeColumn+" IN ("+strings.TrimSuffix(strings.Repeat("?,", len(types)), ",")+")")
		}
		for _, value := range types {
			args = append(args, value)
		}
	}
	if since != nil {
		parts = append(parts, "AND "+timeColumn+" >= ?")
		args = append(args, timelineDBCeil(*since))
	}
	if before != nil {
		parts = append(parts, "AND "+timeColumn+" < ?")
		args = append(args, timelineDBCeil(*before))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " " + strings.Join(parts, " "), args
}

func timelineProjectPredicate(filters []string, since, before *time.Time) (string, []any) {
	if len(filters) == 0 {
		return timelineSourcePredicate("", "pe.created_at", nil, since, before)
	}
	parts := make([]string, 0, len(filters))
	args := make([]any, 0, len(filters)+2)
	for _, filter := range filters {
		if strings.HasSuffix(filter, ".*") {
			prefix := strings.TrimSuffix(filter, "*")
			parts = append(parts, "(pe.type >= ? AND pe.type < ?)")
			args = append(args, prefix, strings.TrimSuffix(prefix, ".")+"/")
		} else {
			parts = append(parts, "pe.type = ?")
			args = append(args, filter)
		}
	}
	clause := " AND (" + strings.Join(parts, " OR ") + ")"
	timeClause, timeArgs := timelineSourcePredicate("", "pe.created_at", nil, since, before)
	return clause + timeClause, append(args, timeArgs...)
}

// timelineScanWindow maps a cursor position to the source's id window. The two
// directions carry opposite positions: ascending holds an exclusive LOWER bound
// under a fixed fence, descending an exclusive UPPER bound walking toward zero.
func timelineScanWindow(desc bool, after, snapshot, before int64) (int64, int64) {
	if desc {
		return 0, before - 1
	}
	return after, snapshot
}

// Stable bind ordering keeps selection sets independent of map iteration.
func timelineSelectionList(values map[string]bool) (string, []any) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		args = append(args, key)
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(keys)), ","), args
}

func loadTimelineAffiliationSet(ctx context.Context, q timelineQueryer, root string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE descendants(uuid) AS (
		SELECT uuid FROM containers WHERE uuid = ?
		UNION ALL
		SELECT c.uuid FROM containers c JOIN descendants d ON c.parent_uuid = d.uuid
	) SELECT uuid FROM descendants ORDER BY uuid`, root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

// hydrateTimelineAddressees fills each message entry's `to` with every
// addressee of that one say. The rows were collapsed to one entry per group, so
// the addressees have to be read back from the group; obligation 'none' (a log
// entry) addresses nobody and keeps an empty list. Handles are sorted so the
// projection is deterministic regardless of scan order.
func hydrateTimelineAddressees(ctx context.Context, tx *sql.Tx, entries []WrkqTimelineEntry) error {
	groups := make([]any, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Message == nil || entry.Message.GroupID == "" || seen[entry.Message.GroupID] {
			continue
		}
		seen[entry.Message.GroupID] = true
		groups = append(groups, entry.Message.GroupID)
	}
	if len(groups) == 0 {
		return nil
	}
	query := `SELECT group_id, COALESCE(to_scope_ref, to_principal_ref)
		    FROM envelopes
		   WHERE group_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(groups)), ",") + `)
		     AND (to_scope_ref IS NOT NULL OR to_principal_ref IS NOT NULL)`
	rows, err := tx.QueryContext(ctx, query, groups...)
	if err != nil {
		return NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	addressees := map[string][]string{}
	for rows.Next() {
		var group, handle string
		if err := rows.Scan(&group, &handle); err != nil {
			return NewInternalError(err)
		}
		addressees[group] = append(addressees[group], handle)
	}
	if err := rows.Err(); err != nil {
		return NewInternalError(err)
	}
	for index := range entries {
		message := entries[index].Message
		if message == nil {
			continue
		}
		if to := addressees[message.GroupID]; len(to) > 0 {
			sort.Strings(to)
			message.To = to
		}
	}
	return nil
}
