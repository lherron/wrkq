//go:build wrkq_local

package workflow

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/selectors"
)

func (s *Service) ListEffects(taskSelector string, all bool) ([]Effect, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	return listEffectsForInstance(s.db, inst.ID, all)
}

// effectColumns is the SELECT list scanEffects reads, in scan order.
const effectColumns = `id, instance_id, revision, COALESCE(sequence,0), kind, payload_json, status, idempotency_key, COALESCE(semantic_key,''), attempts,
	COALESCE(leased_by,''), COALESCE(leased_until,''), COALESCE(delivered_at,''), COALESCE(last_error,''), COALESCE(receipt_json,''),
	created_at, updated_at`

// effectQueueOrder orders effects by their per-instance sequence, unsequenced
// (legacy/supervisor) effects last.
const effectQueueOrder = `ORDER BY COALESCE(sequence, 9223372036854775807), created_at, id`

// queryEffects runs a workflow_effects query whose WHERE/ORDER tail is clause.
func queryEffects(q rowsQueryer, clause string, args ...interface{}) ([]Effect, error) {
	rows, err := q.Query(`SELECT `+effectColumns+` FROM workflow_effects `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEffects(rows)
}

func listEffectsForInstance(q rowsQueryer, instanceID string, all bool) ([]Effect, error) {
	clause := `WHERE instance_id = ?`
	if !all {
		clause += ` AND status IN ('pending','leased','failed','delivered')`
	}
	return queryEffects(q, clause+` `+effectQueueOrder, instanceID)
}

func scanEffects(rows *sql.Rows) ([]Effect, error) {
	var out []Effect
	for rows.Next() {
		var e Effect
		var payload, receipt string
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Revision, &e.Sequence, &e.Kind, &payload, &e.Status, &e.IdempotencyKey, &e.SemanticKey, &e.Attempts, &e.LeasedBy, &e.LeasedUntil, &e.DeliveredAt, &e.LastError, &receipt, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		if payload != "" {
			e.Payload = json.RawMessage(payload)
		}
		if receipt != "" {
			e.Receipt = json.RawMessage(receipt)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) ShowEffect(id string) (*Effect, error) {
	effects, err := queryEffects(s.db, `WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(effects) == 0 {
		return nil, fmt.Errorf("effect not found: %s", id)
	}
	return &effects[0], nil
}

func (s *Service) ClaimEffects(adapter string, limit int, leaseMs int64, taskSelector, kind string) (*EffectClaim, error) {
	if adapter == "" {
		adapter = "wrkf-effect-claim"
	}
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be greater than zero")
	}
	if leaseMs <= 0 {
		return nil, fmt.Errorf("leaseMs must be greater than zero")
	}
	var taskUUID string
	if taskSelector != "" {
		resolved, _, err := selectors.ResolveTask(s.db, taskSelector)
		if err != nil {
			return nil, err
		}
		taskUUID = resolved
	}

	claim, nowText, err := s.newEffectClaim(leaseMs)
	if err != nil {
		return nil, err
	}
	token, expiresAt := claim.LeaseToken, claim.LeaseExpiresAt

	err = withImmediateTx(s.db, func(tx *sql.Tx) error {
		query := `
			SELECT e.id
			FROM workflow_effects e
			JOIN workflow_instances i ON i.id = e.instance_id
			WHERE (
				e.status IN ('pending', 'failed')
				OR (e.status = 'leased' AND COALESCE(e.leased_until, '') <= ?)
			)`
		args := []interface{}{nowText}
		if taskUUID != "" {
			query += ` AND i.task_uuid = ?`
			args = append(args, taskUUID)
		}
		if kind != "" {
			query += ` AND e.kind = ?`
			args = append(args, kind)
		}
		query += ` ORDER BY COALESCE(e.sequence, 9223372036854775807), e.created_at, e.id LIMIT ?`
		args = append(args, limit)

		rows, err := tx.Query(query, args...)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}

		placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
		updateArgs := []interface{}{adapter, expiresAt, token, nowText}
		for _, id := range ids {
			updateArgs = append(updateArgs, id)
		}
		result, err := tx.Exec(fmt.Sprintf(`
			UPDATE workflow_effects
			SET status = 'leased',
			    leased_by = ?,
			    leased_until = ?,
			    lease_token = ?,
			    updated_at = ?
			WHERE id IN (%s)
		`, placeholders), updateArgs...)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected != int64(len(ids)) {
			return leaseConflictError("", token)
		}

		effects, err := effectsByLeaseTokenTx(tx, token)
		if err != nil {
			return err
		}
		claim.Effects = effects
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claim, nil
}

func effectsByLeaseTokenTx(tx *sql.Tx, token string) ([]Effect, error) {
	return queryEffects(tx, `WHERE lease_token = ? `+effectQueueOrder, token)
}

// newEffectClaim mints an empty claim holding a fresh lease token that
// expires leaseMs from now; nowText is the claim instant.
func (s *Service) newEffectClaim(leaseMs int64) (claim *EffectClaim, nowText string, err error) {
	token, err := newLeaseToken()
	if err != nil {
		return nil, "", err
	}
	now := s.now()
	expiresAt := now.Add(time.Duration(leaseMs) * time.Millisecond).Format(time.RFC3339)
	return &EffectClaim{Effects: []Effect{}, LeaseToken: token, LeaseExpiresAt: expiresAt}, now.Format(time.RFC3339), nil
}

func newLeaseToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "lease_" + hex.EncodeToString(b[:]), nil
}

func (s *Service) AckEffect(id, leaseToken string) (*Effect, error) {
	return s.AckEffectWithReceipt(id, leaseToken, nil)
}

func (s *Service) AckEffectWithReceipt(id, leaseToken string, receipt json.RawMessage) (*Effect, error) {
	if len(receipt) > 0 && !json.Valid(receipt) {
		return nil, fmt.Errorf("invalid effect receipt JSON")
	}
	now := s.now().Format(time.RFC3339)
	result, err := s.db.Exec(`
		UPDATE workflow_effects
		SET status = 'delivered',
		    delivered_at = ?,
		    attempts = attempts + 1,
		    leased_by = NULL,
		    leased_until = NULL,
		    lease_token = NULL,
		    last_error = NULL,
		    receipt_json = COALESCE(NULLIF(?, ''), receipt_json),
		    updated_at = ?
		WHERE id = ?
		  AND status = 'leased'
		  AND lease_token = ?
		  AND leased_until > ?
	`, now, string(receipt), now, id, leaseToken, now)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return nil, leaseConflictError(id, leaseToken)
	}
	return s.ShowEffect(id)
}

func (s *Service) ForceAckEffect(id string) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	result, err := s.db.Exec(`
		UPDATE workflow_effects
		SET status = 'delivered',
		    delivered_at = ?,
		    attempts = attempts + 1,
		    leased_by = NULL,
		    leased_until = NULL,
		    lease_token = NULL,
		    last_error = NULL,
		    updated_at = ?
		WHERE id = ?
	`, now, now, id)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return nil, fmt.Errorf("effect not found: %s", id)
	}
	return s.ShowEffect(id)
}

func (s *Service) claimEffectByID(id, adapter string, leaseMs int64) (*EffectClaim, error) {
	if adapter == "" {
		adapter = "wrkf-effect-deliver"
	}
	if leaseMs <= 0 {
		return nil, fmt.Errorf("leaseMs must be greater than zero")
	}
	claim, nowText, err := s.newEffectClaim(leaseMs)
	if err != nil {
		return nil, err
	}
	token, expiresAt := claim.LeaseToken, claim.LeaseExpiresAt
	err = withImmediateTx(s.db, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRow(`SELECT status FROM workflow_effects WHERE id = ?`, id).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("effect not found: %s", id)
			}
			return err
		}
		if status == "delivered" {
			effects, err := effectByIDTx(tx, id)
			if err != nil {
				return err
			}
			claim.Effects = effects
			claim.LeaseToken = ""
			claim.LeaseExpiresAt = ""
			return nil
		}
		result, err := tx.Exec(`
			UPDATE workflow_effects
			SET status = 'leased',
			    leased_by = ?,
			    leased_until = ?,
			    lease_token = ?,
			    updated_at = ?
			WHERE id = ?
			  AND (
				status IN ('pending', 'failed')
				OR (status = 'leased' AND COALESCE(leased_until, '') <= ?)
			  )
		`, adapter, expiresAt, token, nowText, id, nowText)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected != 1 {
			return effectNotDeliverableError(id, status)
		}
		effects, err := effectsByLeaseTokenTx(tx, token)
		if err != nil {
			return err
		}
		claim.Effects = effects
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claim, nil
}

func effectByIDTx(tx *sql.Tx, id string) ([]Effect, error) {
	return queryEffects(tx, `WHERE id = ?`, id)
}

func (s *Service) FailEffect(id, leaseToken, reason string, retryable bool) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	result, err := s.db.Exec(`
		UPDATE workflow_effects
		SET status = 'failed',
		    attempts = attempts + 1,
		    last_error = ?,
		    leased_by = NULL,
		    leased_until = NULL,
		    lease_token = NULL,
		    updated_at = ?
		WHERE id = ?
		  AND status = 'leased'
		  AND lease_token = ?
		  AND leased_until > ?
	`, reason, now, id, leaseToken, now)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return nil, leaseConflictError(id, leaseToken)
	}
	return s.ShowEffect(id)
}

func (s *Service) ForceFailEffect(id, reason string) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	result, err := s.db.Exec(`
		UPDATE workflow_effects
		SET status = 'failed',
		    attempts = attempts + 1,
		    last_error = ?,
		    leased_by = NULL,
		    leased_until = NULL,
		    lease_token = NULL,
		    updated_at = ?
		WHERE id = ?
	`, reason, now, id)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return nil, fmt.Errorf("effect not found: %s", id)
	}
	return s.ShowEffect(id)
}

func (s *Service) RetryEffect(id string) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE workflow_effects SET status = 'pending', leased_by = NULL, leased_until = NULL, lease_token = NULL, last_error = NULL, updated_at = ? WHERE id = ?`, now, id); err != nil {
		return nil, err
	}
	return s.ShowEffect(id)
}

func (s *Service) SupervisorCall(taskSelector, reason string) (*Effect, error) {
	return s.insertSupervisorEffect(taskSelector, "supervisor_call", "supervisor", reason)
}

func (s *Service) SupervisorEscalate(taskSelector, reason string) (*Effect, error) {
	return s.insertSupervisorEffect(taskSelector, "supervisor_escalation", "escalate", reason)
}

// insertSupervisorEffect enqueues an unsequenced supervisor effect at the
// instance's current revision; keyTag namespaces its idempotency key.
func (s *Service) insertSupervisorEffect(taskSelector, kind, keyTag, reason string) (*Effect, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	var effect *Effect
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		id, err := nextSeqID(tx, "workflow_effect_seq", "eff")
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"reason": reason})
		key := fmt.Sprintf("%s:%d:%s:%s", inst.ID, inst.Revision, keyTag, id)
		_, err = tx.Exec(`INSERT INTO workflow_effects (id, instance_id, revision, kind, payload_json, status, idempotency_key) VALUES (?, ?, ?, ?, ?, 'pending', ?)`, id, inst.ID, inst.Revision, kind, string(payload), key)
		if err != nil {
			return err
		}
		effect = &Effect{ID: id, InstanceID: inst.ID, Revision: inst.Revision, Kind: kind, Payload: payload, Status: "pending", IdempotencyKey: key}
		return nil
	})
	return effect, err
}
