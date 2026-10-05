//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// terminalizeActiveRunsTx is the shared authority fence for explicit instance
// terminalization. The caller supplies the identity of the terminal instance
// event so every run records the same durable cause. It must run inside the
// caller's immediate transaction.
func terminalizeActiveRunsTx(tx *sql.Tx, inst *Instance, disposition, terminalEventID, completedAt string) ([]TerminalizedRunSummary, error) {
	active, err := queryRuns(tx, `WHERE instance_id = ? AND status = 'active' ORDER BY started_at, id`, inst.ID)
	if err != nil {
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
	return enqueueRenderedEffectsTx(tx, resolved, specs, disposition, "", now)
}
