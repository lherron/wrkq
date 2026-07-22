package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// TerminalizedRunSummary is the token-free readback for a run whose authority
// ended because its workflow instance was explicitly terminalized.
type TerminalizedRunSummary struct {
	RunID          string `json:"runId"`
	Status         string `json:"status"`
	CompletedAt    string `json:"completedAt"`
	TerminalResult string `json:"terminalResult"`
}

// terminalizeActiveRunsTx is the shared authority fence for explicit instance
// terminalization. The caller supplies the identity of the terminal instance
// event so every run records the same durable cause. It must run inside the
// caller's immediate transaction.
func terminalizeActiveRunsTx(tx *sql.Tx, inst *Instance, disposition, terminalEventID, completedAt string) ([]TerminalizedRunSummary, error) {
	rows, err := tx.Query(`
		SELECT id, instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       COALESCE(action,''), status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
		       COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,'')
		FROM workflow_runs WHERE instance_id = ? AND status = 'active' ORDER BY started_at, id
	`, inst.ID)
	if err != nil {
		return nil, err
	}
	var active []Run
	for rows.Next() {
		var run Run
		if err := rows.Scan(&run.ID, &run.InstanceID, &run.Role, &run.PrincipalRef, &run.DeliveryRef, &run.Lane, &run.ExternalRunRef, &run.Action, &run.Status, &run.StartedAt, &run.CompletedAt, &run.TerminalResult, &run.LeaseOwner, &run.LeaseToken, &run.LeaseExpiresAt, &run.HeartbeatAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		active = append(active, run)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	summaries := make([]TerminalizedRunSummary, 0, len(active))
	for i := range active {
		run := &active[i]
		causeJSON, err := json.Marshal(map[string]interface{}{
			"cause":       "instance_terminalized",
			"disposition": disposition,
			"eventId":     terminalEventID,
		})
		if err != nil {
			return nil, err
		}
		cause := string(causeJSON)
		res, err := tx.Exec(`
			UPDATE workflow_runs
			SET status = 'cancelled', terminal_result = ?, completed_at = ?, lease_token = NULL
			WHERE id = ? AND status = 'active'
		`, cause, completedAt, run.ID)
		if err != nil {
			return nil, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, fmt.Errorf("terminalize active run %s: expected one row, updated %d", run.ID, affected)
		}
		run.Status = "cancelled"
		run.TerminalResult = cause
		run.CompletedAt = completedAt
		run.LeaseToken = ""
		if _, err := insertEventReturning(tx, inst.ID, "workflow.run_finished", run.PrincipalRef, run.Role, run.ID, inst.Revision, inst.Revision, "", taskDocEtagInt(inst), inst.TaskDocHash, runLifecyclePayload(run)); err != nil {
			return nil, err
		}
		summaries = append(summaries, TerminalizedRunSummary{
			RunID: run.ID, Status: run.Status, CompletedAt: run.CompletedAt, TerminalResult: run.TerminalResult,
		})
	}
	return summaries, nil
}

func createDispositionEffectsTx(tx *sql.Tx, tpl *Template, resolved Instance, disposition, now string) ([]Effect, error) {
	var specs []EffectSpec
	if tpl.Suspension != nil {
		specs = tpl.Suspension.Effects[disposition]
	}
	created := make([]Effect, 0, len(specs))
	for _, spec := range specs {
		id, err := nextSeqID(tx, "workflow_effect_seq", "eff")
		if err != nil {
			return nil, err
		}
		seq, err := nextEffectSequenceTx(tx, resolved.ID)
		if err != nil {
			return nil, err
		}
		rendered, semanticKey, err := renderEffectSpec(spec, effectRenderContext{instance: resolved, outcomeID: disposition, sequence: seq})
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(rendered)
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%s:%s", resolved.ID, semanticKey)
		if _, err := tx.Exec(`
			INSERT INTO workflow_effects (id, instance_id, revision, sequence, kind, payload_json, status, idempotency_key, semantic_key, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)
		`, id, resolved.ID, resolved.Revision, seq, rendered.Kind, string(payload), key, semanticKey, now, now); err != nil {
			return nil, err
		}
		created = append(created, Effect{
			ID: id, InstanceID: resolved.ID, Revision: resolved.Revision, Sequence: seq, Kind: rendered.Kind,
			Payload: json.RawMessage(payload), Status: "pending", IdempotencyKey: key, SemanticKey: semanticKey,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return created, nil
}
