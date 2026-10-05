//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/db"
)

// checkRunColumns is the SELECT list scanCheckRun reads, in scan order.
const checkRunColumns = `id, instance_id, transition_id, check_id, COALESCE(hook_id,''), input_hash, exit_code, verdict,
	outcome, code, summary, facts_json, COALESCE(principal_ref, actor, ''), COALESCE(role,''), started_at, completed_at`

func scanCheckRun(scanner runRowScanner) (CheckRun, error) {
	var c CheckRun
	var exit sql.NullInt64
	var hook, outcome, code, summary, facts, actor, role, completed sql.NullString
	if err := scanner.Scan(&c.ID, &c.InstanceID, &c.TransitionID, &c.CheckID, &hook, &c.InputHash, &exit, &c.Verdict, &outcome, &code, &summary, &facts, &actor, &role, &c.StartedAt, &completed); err != nil {
		return c, err
	}
	c.HookID = hook.String
	if exit.Valid {
		v := int(exit.Int64)
		c.ExitCode = &v
	}
	c.Outcome, c.Code, c.Summary = outcome.String, code.String, summary.String
	if facts.Valid {
		c.Facts = json.RawMessage(facts.String)
	}
	c.PrincipalRef, c.Role, c.CompletedAt = actor.String, role.String, completed.String
	return c, nil
}

func (s *Service) ShowCheckRun(id string) (*CheckRun, error) {
	c, err := scanCheckRun(s.db.QueryRow(`SELECT `+checkRunColumns+` FROM workflow_check_runs WHERE id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("check run not found: %s", id)
		}
		return nil, err
	}
	return &c, nil
}

func (s *Service) ListCheckRuns(taskSelector, transitionID string) ([]CheckRun, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + checkRunColumns + ` FROM workflow_check_runs WHERE instance_id = ?`
	args := []interface{}{inst.ID}
	if transitionID != "" {
		query += ` AND transition_id = ?`
		args = append(args, transitionID)
	}
	query += ` ORDER BY started_at, id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CheckRun
	for rows.Next() {
		c, err := scanCheckRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// latestCheckFor returns the most recent run of checkID for the transition;
// ok is false when none was recorded (or the read failed).
func latestCheckFor(database *db.DB, instanceID, transitionID, checkID string) (CheckRun, bool) {
	c, err := scanCheckRun(database.QueryRow(`
		SELECT `+checkRunColumns+`
		FROM workflow_check_runs
		WHERE instance_id = ? AND transition_id = ? AND check_id = ?
		ORDER BY started_at DESC, id DESC LIMIT 1
	`, instanceID, transitionID, checkID))
	return c, err == nil
}
