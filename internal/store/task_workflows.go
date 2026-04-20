package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
)

// CreateRoleAssignmentParams contains parameters for creating a task role assignment.
type CreateRoleAssignmentParams struct {
	TaskUUID   string
	Role       string
	ActorUUID  string
	AssignedAt *string
}

// CreateEvidenceItemParams contains parameters for creating a task evidence item.
type CreateEvidenceItemParams struct {
	TaskUUID            string
	Kind                string
	Ref                 string
	ContentHash         *string
	ProducedByActorUUID string
	ProducedByRole      string
	BuildID             *string
	BuildVersion        *string
	BuildEnv            *string
	ProducedAt          *string
	Meta                *string
}

// CreateTaskTransitionParams contains parameters for creating a task transition.
type CreateTaskTransitionParams struct {
	TaskUUID           string
	FromPhase          *string
	ToPhase            string
	FromLifecycleState *string
	ToLifecycleState   *string
	ActorUUID          string
	ActorRole          string
	EvidenceItemUUIDs  *string
	TransitionedAt     *string
	Meta               *string
}

// CreateRoleAssignment inserts a task role assignment and returns the stored record.
func (ts *TaskStore) CreateRoleAssignment(params CreateRoleAssignmentParams) (*domain.TaskRoleAssignment, error) {
	var assignment *domain.TaskRoleAssignment

	err := ts.store.withTx(func(tx *sql.Tx, _ *events.Writer) error {
		var (
			res sql.Result
			err error
		)

		if params.AssignedAt != nil {
			res, err = tx.Exec(`
				INSERT INTO task_role_assignments (task_uuid, role, actor_uuid, assigned_at)
				VALUES (?, ?, ?, ?)
			`, params.TaskUUID, params.Role, params.ActorUUID, *params.AssignedAt)
		} else {
			res, err = tx.Exec(`
				INSERT INTO task_role_assignments (task_uuid, role, actor_uuid)
				VALUES (?, ?, ?)
			`, params.TaskUUID, params.Role, params.ActorUUID)
		}
		if err != nil {
			return fmt.Errorf("failed to create task role assignment: %w", err)
		}

		rowID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get task role assignment row id: %w", err)
		}

		assignment, err = getRoleAssignmentByRowID(tx, rowID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return assignment, nil
}

// ListRoleAssignments returns the workflow role assignments for a task.
func (ts *TaskStore) ListRoleAssignments(taskUUID string) ([]domain.TaskRoleAssignment, error) {
	rows, err := ts.store.db.Query(`
		SELECT uuid, task_uuid, role, actor_uuid, assigned_at
		FROM task_role_assignments
		WHERE task_uuid = ?
		ORDER BY role
	`, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query task role assignments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	assignments := make([]domain.TaskRoleAssignment, 0)
	for rows.Next() {
		assignment, err := scanRoleAssignment(rows)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, *assignment)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating task role assignments: %w", err)
	}

	return assignments, nil
}

// CreateEvidenceItem inserts a task evidence item and returns the stored record.
func (ts *TaskStore) CreateEvidenceItem(params CreateEvidenceItemParams) (*domain.EvidenceItem, error) {
	var item *domain.EvidenceItem

	err := ts.store.withTx(func(tx *sql.Tx, _ *events.Writer) error {
		var (
			res sql.Result
			err error
		)

		if params.ProducedAt != nil {
			res, err = tx.Exec(`
				INSERT INTO evidence_items (
					task_uuid, kind, ref, content_hash, produced_by_actor_uuid, produced_by_role,
					build_id, build_version, build_env, produced_at, meta
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, params.TaskUUID, params.Kind, params.Ref, params.ContentHash, params.ProducedByActorUUID,
				params.ProducedByRole, params.BuildID, params.BuildVersion, params.BuildEnv, *params.ProducedAt, params.Meta)
		} else {
			res, err = tx.Exec(`
				INSERT INTO evidence_items (
					task_uuid, kind, ref, content_hash, produced_by_actor_uuid, produced_by_role,
					build_id, build_version, build_env, meta
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, params.TaskUUID, params.Kind, params.Ref, params.ContentHash, params.ProducedByActorUUID,
				params.ProducedByRole, params.BuildID, params.BuildVersion, params.BuildEnv, params.Meta)
		}
		if err != nil {
			return fmt.Errorf("failed to create evidence item: %w", err)
		}

		rowID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get evidence item row id: %w", err)
		}

		item, err = getEvidenceItemByRowID(tx, rowID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return item, nil
}

// ListEvidenceItems returns evidence items for a task.
func (ts *TaskStore) ListEvidenceItems(taskUUID string) ([]domain.EvidenceItem, error) {
	rows, err := ts.store.db.Query(`
		SELECT uuid, id, task_uuid, kind, ref, content_hash, produced_by_actor_uuid, produced_by_role,
		       build_id, build_version, build_env, produced_at, meta
		FROM evidence_items
		WHERE task_uuid = ?
		ORDER BY produced_at, id
	`, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query evidence items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]domain.EvidenceItem, 0)
	for rows.Next() {
		item, err := scanEvidenceItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating evidence items: %w", err)
	}

	return items, nil
}

// CreateTaskTransition inserts a task transition and returns the stored record.
func (ts *TaskStore) CreateTaskTransition(params CreateTaskTransitionParams) (*domain.TaskTransition, error) {
	var transition *domain.TaskTransition

	err := ts.store.withTx(func(tx *sql.Tx, _ *events.Writer) error {
		var (
			res sql.Result
			err error
		)

		if params.TransitionedAt != nil {
			res, err = tx.Exec(`
				INSERT INTO task_transitions (
					task_uuid, from_phase, to_phase, from_lifecycle_state, to_lifecycle_state,
					actor_uuid, actor_role, evidence_item_uuids, transitioned_at, meta
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, params.TaskUUID, params.FromPhase, params.ToPhase, params.FromLifecycleState,
				params.ToLifecycleState, params.ActorUUID, params.ActorRole, params.EvidenceItemUUIDs,
				*params.TransitionedAt, params.Meta)
		} else {
			res, err = tx.Exec(`
				INSERT INTO task_transitions (
					task_uuid, from_phase, to_phase, from_lifecycle_state, to_lifecycle_state,
					actor_uuid, actor_role, evidence_item_uuids, meta
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, params.TaskUUID, params.FromPhase, params.ToPhase, params.FromLifecycleState,
				params.ToLifecycleState, params.ActorUUID, params.ActorRole, params.EvidenceItemUUIDs,
				params.Meta)
		}
		if err != nil {
			return fmt.Errorf("failed to create task transition: %w", err)
		}

		rowID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get task transition row id: %w", err)
		}

		transition, err = getTaskTransitionByRowID(tx, rowID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return transition, nil
}

// ListTaskTransitions returns workflow transitions for a task.
func (ts *TaskStore) ListTaskTransitions(taskUUID string) ([]domain.TaskTransition, error) {
	rows, err := ts.store.db.Query(`
		SELECT uuid, id, task_uuid, from_phase, to_phase, from_lifecycle_state, to_lifecycle_state,
		       actor_uuid, actor_role, evidence_item_uuids, transitioned_at, meta
		FROM task_transitions
		WHERE task_uuid = ?
		ORDER BY transitioned_at, id
	`, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query task transitions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	transitions := make([]domain.TaskTransition, 0)
	for rows.Next() {
		transition, err := scanTaskTransition(rows)
		if err != nil {
			return nil, err
		}
		transitions = append(transitions, *transition)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating task transitions: %w", err)
	}

	return transitions, nil
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func getRoleAssignmentByRowID(tx *sql.Tx, rowID int64) (*domain.TaskRoleAssignment, error) {
	row := tx.QueryRow(`
		SELECT uuid, task_uuid, role, actor_uuid, assigned_at
		FROM task_role_assignments
		WHERE rowid = ?
	`, rowID)
	return scanRoleAssignment(row)
}

func scanRoleAssignment(scanner rowScanner) (*domain.TaskRoleAssignment, error) {
	var assignment domain.TaskRoleAssignment
	var assignedAt string
	if err := scanner.Scan(&assignment.UUID, &assignment.TaskUUID, &assignment.Role, &assignment.ActorUUID, &assignedAt); err != nil {
		return nil, fmt.Errorf("failed to scan task role assignment: %w", err)
	}
	parsed, err := parseStoredTime(assignedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to parse assigned_at: %w", err)
	}
	assignment.AssignedAt = parsed
	return &assignment, nil
}

func getEvidenceItemByRowID(tx *sql.Tx, rowID int64) (*domain.EvidenceItem, error) {
	row := tx.QueryRow(`
		SELECT uuid, id, task_uuid, kind, ref, content_hash, produced_by_actor_uuid, produced_by_role,
		       build_id, build_version, build_env, produced_at, meta
		FROM evidence_items
		WHERE rowid = ?
	`, rowID)
	return scanEvidenceItem(row)
}

func scanEvidenceItem(scanner rowScanner) (*domain.EvidenceItem, error) {
	var item domain.EvidenceItem
	var producedAt string
	if err := scanner.Scan(
		&item.UUID, &item.ID, &item.TaskUUID, &item.Kind, &item.Ref, &item.ContentHash,
		&item.ProducedByActorUUID, &item.ProducedByRole, &item.BuildID, &item.BuildVersion,
		&item.BuildEnv, &producedAt, &item.Meta,
	); err != nil {
		return nil, fmt.Errorf("failed to scan evidence item: %w", err)
	}
	parsed, err := parseStoredTime(producedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to parse produced_at: %w", err)
	}
	item.ProducedAt = parsed
	return &item, nil
}

func getTaskTransitionByRowID(tx *sql.Tx, rowID int64) (*domain.TaskTransition, error) {
	row := tx.QueryRow(`
		SELECT uuid, id, task_uuid, from_phase, to_phase, from_lifecycle_state, to_lifecycle_state,
		       actor_uuid, actor_role, evidence_item_uuids, transitioned_at, meta
		FROM task_transitions
		WHERE rowid = ?
	`, rowID)
	return scanTaskTransition(row)
}

func scanTaskTransition(scanner rowScanner) (*domain.TaskTransition, error) {
	var transition domain.TaskTransition
	var transitionedAt string
	if err := scanner.Scan(
		&transition.UUID, &transition.ID, &transition.TaskUUID, &transition.FromPhase, &transition.ToPhase,
		&transition.FromLifecycleState, &transition.ToLifecycleState, &transition.ActorUUID,
		&transition.ActorRole, &transition.EvidenceItemUUIDs, &transitionedAt, &transition.Meta,
	); err != nil {
		return nil, fmt.Errorf("failed to scan task transition: %w", err)
	}
	parsed, err := parseStoredTime(transitionedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to parse transitioned_at: %w", err)
	}
	transition.TransitionedAt = parsed
	return &transition, nil
}

func parseStoredTime(value string) (time.Time, error) {
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", value)
}
