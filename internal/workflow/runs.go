//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Service) StartRun(taskSelector, role, actor string, opts StartRunOptions) (*Run, error) {
	return s.StartRunForSelectors(taskSelector, "", role, actor, opts)
}

func (s *Service) StartRunForSelectors(taskSelector, instanceID, role, actor string, opts StartRunOptions) (*Run, error) {
	var run *Run
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, taskSelector, instanceID)
		if err != nil {
			return err
		}
		requestHash := runStartRequestHash(inst.ID, role, actor, opts)
		if opts.IdempotencyKey != "" {
			existing, existingHash, err := selectRunByInstanceIdempotencyKey(tx, inst.ID, opts.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing != nil {
				if existingHash != requestHash {
					return idempotencyMismatchError(opts.IdempotencyKey)
				}
				run = existing
				return nil
			}
		}

		id, err := nextSeqID(tx, "workflow_run_seq", "run")
		if err != nil {
			return err
		}
		now := s.now().Format(time.RFC3339)
		_, err = tx.Exec(`
			INSERT INTO workflow_runs (
				id, instance_id, role, actor, principal_ref, delivery_ref, lane, external_run_ref,
				status, started_at, idempotency_key, request_hash, action,
				lease_owner, lease_token, lease_expires_at, heartbeat_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, inst.ID, role, actor, actor, nullIfEmpty(opts.DeliveryRef), nullIfEmpty(opts.Lane), nullIfEmpty(opts.ExternalRunRef), now, nullIfEmpty(opts.IdempotencyKey), nullIfEmpty(requestHash), nullIfEmpty(opts.Action), nullIfEmpty(opts.LeaseOwner), nullIfEmpty(opts.LeaseToken), nullIfEmpty(opts.LeaseExpiresAt), nullIfEmpty(opts.HeartbeatAt))
		if err != nil {
			if isRunUniqueConflict(err) {
				return idempotencyMismatchError(opts.IdempotencyKey)
			}
			return err
		}
		_, err = tx.Exec(`
			INSERT INTO workflow_role_bindings (instance_id, role, actor, principal_ref, delivery_ref, lane, binding_mode, bound_at)
			VALUES (?, ?, ?, ?, ?, ?, 'auto', ?)
			ON CONFLICT(instance_id, role, actor) DO UPDATE SET
				principal_ref = excluded.principal_ref,
				delivery_ref = COALESCE(excluded.delivery_ref, workflow_role_bindings.delivery_ref),
				lane = COALESCE(excluded.lane, workflow_role_bindings.lane)
		`, inst.ID, role, actor, actor, nullIfEmpty(opts.DeliveryRef), nullIfEmpty(opts.Lane), now)
		if err != nil {
			return err
		}
		run = &Run{ID: id, InstanceID: inst.ID, Role: role, PrincipalRef: actor, DeliveryRef: opts.DeliveryRef, Lane: opts.Lane, ExternalRunRef: opts.ExternalRunRef, Action: opts.Action, Status: "active", StartedAt: now, LeaseOwner: opts.LeaseOwner, LeaseToken: opts.LeaseToken, LeaseExpiresAt: opts.LeaseExpiresAt, HeartbeatAt: opts.HeartbeatAt}
		if _, err := insertEventReturning(tx, inst.ID, "workflow.run_started", actor, role, id, inst.Revision, inst.Revision, opts.IdempotencyKey, taskDocEtagInt(inst), inst.TaskDocHash, runLifecyclePayload(run)); err != nil {
			return err
		}
		return nil
	})
	return run, err
}

func (s *Service) BindExternal(id, externalRunRef string, opts BindExternalOptions) (*Run, error) {
	if strings.TrimSpace(externalRunRef) == "" {
		return nil, fmt.Errorf("externalRunRef is required")
	}
	var run *Run
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		current, err := selectRunByID(tx, id)
		if err != nil {
			return err
		}
		if current.ExternalRunRef != "" && current.ExternalRunRef != externalRunRef {
			return idempotencyMismatchError(opts.IdempotencyKey)
		}
		if opts.DeliveryRef != "" && current.DeliveryRef != "" && current.DeliveryRef != opts.DeliveryRef {
			return idempotencyMismatchError(opts.IdempotencyKey)
		}
		if opts.Lane != "" && current.Lane != "" && current.Lane != opts.Lane {
			return idempotencyMismatchError(opts.IdempotencyKey)
		}
		deliveryRef := current.DeliveryRef
		if opts.DeliveryRef != "" {
			deliveryRef = opts.DeliveryRef
		}
		lane := current.Lane
		if opts.Lane != "" {
			lane = opts.Lane
		}
		_, err = tx.Exec(`
			UPDATE workflow_runs
			SET external_run_ref = ?, delivery_ref = ?, lane = ?
			WHERE id = ?
		`, externalRunRef, nullIfEmpty(deliveryRef), nullIfEmpty(lane), id)
		if err != nil {
			if isRunUniqueConflict(err) {
				return idempotencyMismatchError(opts.IdempotencyKey)
			}
			return err
		}
		_, err = tx.Exec(`
			UPDATE workflow_role_bindings
			SET delivery_ref = COALESCE(NULLIF(?, ''), delivery_ref), lane = COALESCE(NULLIF(?, ''), lane)
			WHERE instance_id = ? AND role = ? AND principal_ref = ?
		`, deliveryRef, lane, current.InstanceID, current.Role, current.PrincipalRef)
		if err != nil {
			return err
		}
		current.ExternalRunRef = externalRunRef
		current.DeliveryRef = deliveryRef
		current.Lane = lane
		run = current
		return nil
	})
	return run, err
}

func (s *Service) FinishRun(id, status, summary string) (*Run, error) {
	if status == "" {
		status = "completed"
	}
	now := s.now().Format(time.RFC3339)
	var run *Run
	if err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		current, err := selectRunByID(tx, id)
		if err != nil {
			return err
		}
		if isTerminalRunStatus(current.Status) {
			if current.Status == status && current.TerminalResult == summary {
				run = current
				return nil
			}
			return idempotencyMismatchError(id)
		}
		if _, err := tx.Exec(`UPDATE workflow_runs SET status = ?, terminal_result = ?, completed_at = ?, lease_token = NULL WHERE id = ?`, status, summary, now, id); err != nil {
			return err
		}
		current.Status = status
		current.TerminalResult = summary
		current.CompletedAt = now
		current.LeaseToken = ""
		inst, err := instanceByIDQuery(tx, current.InstanceID)
		if err != nil {
			return err
		}
		if _, err := insertEventReturning(tx, current.InstanceID, "workflow.run_finished", current.PrincipalRef, current.Role, current.ID, inst.Revision, inst.Revision, "", taskDocEtagInt(inst), inst.TaskDocHash, runLifecyclePayload(current)); err != nil {
			return err
		}
		run = current
		return nil
	}); err != nil {
		return nil, err
	}
	return run, nil
}

func (s *Service) FailRun(id, summary string) (*Run, error) {
	return s.FinishRun(id, "failed", summary)
}

func (s *Service) ShowRun(id string) (*Run, error) {
	return selectRunByID(s.db, id)
}

// runColumns is the SELECT list runScanDest binds, in scan order.
const runColumns = `id, instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
	COALESCE(action,''), status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
	COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,'')`

func runScanDest(r *Run) []interface{} {
	return []interface{}{&r.ID, &r.InstanceID, &r.Role, &r.PrincipalRef, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Action, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult, &r.LeaseOwner, &r.LeaseToken, &r.LeaseExpiresAt, &r.HeartbeatAt}
}

func scanRun(scanner runRowScanner) (*Run, error) {
	var r Run
	if err := scanner.Scan(runScanDest(&r)...); err != nil {
		return nil, err
	}
	return &r, nil
}

// queryRuns runs a workflow_runs query whose WHERE/ORDER tail is clause.
func queryRuns(q rowsQueryer, clause string, args ...interface{}) ([]Run, error) {
	rows, err := q.Query(`SELECT `+runColumns+` FROM workflow_runs `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func selectRunByID(q queryer, id string) (*Run, error) {
	run, err := scanRun(q.QueryRow(`SELECT `+runColumns+` FROM workflow_runs WHERE id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found: %s", id)
		}
		return nil, err
	}
	return run, nil
}

func selectRunByInstanceIdempotencyKey(tx *sql.Tx, instanceID, key string) (*Run, string, error) {
	var r Run
	var requestHash string
	err := tx.QueryRow(`
		SELECT `+runColumns+`, COALESCE(request_hash,'')
		FROM workflow_runs WHERE instance_id = ? AND idempotency_key = ?
	`, instanceID, key).Scan(append(runScanDest(&r), &requestHash)...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, "", nil
		}
		return nil, "", err
	}
	return &r, requestHash, nil
}

func runStartRequestHash(instanceID, role, actor string, opts StartRunOptions) string {
	payload := struct {
		InstanceID      string `json:"instanceId"`
		Role            string `json:"role"`
		PrincipalRef    string `json:"principal_ref"`
		DeliveryRef     string `json:"deliveryRef,omitempty"`
		Lane            string `json:"lane,omitempty"`
		ExternalRunRef  string `json:"externalRunRef,omitempty"`
		Action          string `json:"action,omitempty"`
		LeaseOwner      string `json:"leaseOwner,omitempty"`
		LeaseMs         int64  `json:"leaseMs,omitempty"`
		LeaseRequested  bool   `json:"leaseRequested,omitempty"`
		IdempotencySalt string `json:"idempotencySalt"`
	}{
		InstanceID:      instanceID,
		Role:            role,
		PrincipalRef:    actor,
		DeliveryRef:     opts.DeliveryRef,
		Lane:            opts.Lane,
		ExternalRunRef:  opts.ExternalRunRef,
		Action:          opts.Action,
		LeaseOwner:      opts.LeaseOwner,
		LeaseMs:         opts.LeaseMs,
		LeaseRequested:  opts.LeaseOwner != "" || opts.LeaseExpiresAt != "",
		IdempotencySalt: "workflow.run.start.v1",
	}
	b, _ := json.Marshal(payload)
	return Hash(b)
}

func isTerminalRunStatus(status string) bool {
	return status != "" && status != "active"
}

func isRunUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") &&
		(strings.Contains(msg, "workflow_runs.external_run_ref") ||
			strings.Contains(msg, "workflow_runs.instance_id") ||
			strings.Contains(msg, "workflow_runs.idempotency_key"))
}

func (s *Service) ListRuns(taskSelector string) ([]Run, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	return queryRuns(s.db, `WHERE instance_id = ? ORDER BY started_at, id`, inst.ID)
}
