package store

import (
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

func blockerInfosForTask(tx *sql.Tx, taskUUID string) ([]webhooks.BlockerInfo, error) {
	rows, err := tx.Query(`
		SELECT t.id, t.state
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		WHERE r.to_task_uuid = ?
		  AND r.kind = 'blocks'
		  AND t.state NOT IN `+incompleteBlockerExcludedStates+`
		ORDER BY t.id
	`, taskUUID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var blockers []webhooks.BlockerInfo
	for rows.Next() {
		var blocker webhooks.BlockerInfo
		if err := rows.Scan(&blocker.ID, &blocker.State); err != nil {
			return nil, err
		}
		blockers = append(blockers, blocker)
	}
	return blockers, rows.Err()
}

// BlockingTask represents a lightweight view of a task that is blocking another task.
// Used by BlockedBy to return only essential info for dependency checking.
type BlockingTask struct {
	UUID  string `json:"uuid"`
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	State string `json:"state"`
}

// BlockedBy returns all incomplete tasks that are blocking the given task.
// A task is considered "blocking" if there is a 'blocks' relation where
// the blocking task is the source (from_task_uuid) and the given task is the target (to_task_uuid).
// A task is considered "incomplete" if its state is NOT in: completed, archived, deleted, cancelled.
// Tasks in 'idea' state are also excluded as they represent uncommitted work.
func (ts *TaskStore) BlockedBy(taskUUID string) ([]BlockingTask, error) {
	rows, err := ts.store.db.Query(`
		SELECT t.uuid, t.id, t.slug, t.title, t.state
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		WHERE r.to_task_uuid = ?
		  AND r.kind = 'blocks'
		  AND t.state NOT IN `+incompleteBlockerExcludedStates+`
		ORDER BY t.id
	`, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query blocking tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var blockers []BlockingTask
	for rows.Next() {
		var b BlockingTask
		if err := rows.Scan(&b.UUID, &b.ID, &b.Slug, &b.Title, &b.State); err != nil {
			return nil, fmt.Errorf("failed to scan blocking task: %w", err)
		}
		blockers = append(blockers, b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating blocking tasks: %w", err)
	}

	// Return empty slice instead of nil for consistency
	if blockers == nil {
		blockers = []BlockingTask{}
	}

	return blockers, nil
}

// isCompletionState returns true if the given state represents a "completed" blocker
// that should no longer block other tasks.
func isCompletionState(state string) bool {
	switch state {
	case "completed", "cancelled", "archived", "deleted":
		return true
	default:
		return false
	}
}

// incompleteBlockerExcludedStates are the blocker states that no longer block:
// completion states plus idea, which is uncommitted work.
const incompleteBlockerExcludedStates = `('completed', 'archived', 'deleted', 'cancelled', 'idea')`

// blockedDependent is a task the mutated task blocks, with its incomplete
// blockers before and after the mutation.
type blockedDependent struct {
	uuid           string
	blockersBefore []webhooks.BlockerInfo
	blockersAfter  []webhooks.BlockerInfo
}

// blockedDependentsTx lists, in relation order, the tasks taskUUID blocks,
// each with its current incomplete blockers.
func blockedDependentsTx(tx *sql.Tx, taskUUID string) ([]blockedDependent, error) {
	rows, err := tx.Query(`
		SELECT to_task_uuid
		FROM task_relations
		WHERE from_task_uuid = ?
		  AND kind = 'blocks'
	`, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query blocked tasks: %w", err)
	}
	var dependents []blockedDependent
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to scan blocked task: %w", err)
		}
		dependents = append(dependents, blockedDependent{uuid: uuid})
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating blocked tasks: %w", err)
	}
	for i := range dependents {
		if dependents[i].blockersBefore, err = blockerInfosForTask(tx, dependents[i].uuid); err != nil {
			return nil, fmt.Errorf("failed to load blockers for task %s: %w", dependents[i].uuid, err)
		}
	}
	return dependents, nil
}

// newlyUnblockedTx re-reads each dependent's blockers after the mutation and
// keeps the ones left with none.
func newlyUnblockedTx(tx *sql.Tx, dependents []blockedDependent) ([]blockedDependent, error) {
	var unblocked []blockedDependent
	for _, d := range dependents {
		after, err := blockerInfosForTask(tx, d.uuid)
		if err != nil {
			return nil, fmt.Errorf("failed to count blockers for task %s: %w", d.uuid, err)
		}
		if len(after) == 0 {
			d.blockersAfter = after
			unblocked = append(unblocked, d)
		}
	}
	return unblocked, nil
}

// logUnblockedTx records task.unblocked for each newly unblocked dependent and
// returns the webhooks to fire after commit.
func logUnblockedTx(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, via, causeTaskUUID, causeState string, unblocked []blockedDependent) ([]pendingWebhook, error) {
	var hooks []pendingWebhook
	for _, d := range unblocked {
		meta, err := logTaskEvent(tx, ew, attr, d.uuid, "task.unblocked", nil, map[string]interface{}{
			"cause_task_uuid": causeTaskUUID,
			"cause_state":     causeState,
		})
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, pendingWebhook{
			taskUUID: d.uuid,
			ctx: webhooks.EventContext{
				Metadata:     meta,
				Event:        "unblocked",
				PrincipalRef: attr.PrincipalRef,
				Via:          via,
				Transition:   nil,
				Changed:      []string{"blocked_by"},
				Changes: map[string]webhooks.Change{
					"blocked_by": {From: d.blockersBefore, To: d.blockersAfter},
				},
			},
		})
	}
	return hooks, nil
}
