//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/selectors"
)

func validateLedgerAppend(p AppendLedgerParams) error {
	if strings.TrimSpace(p.TaskID) == "" {
		return validationError("taskId", "taskId is required", "task selector", nil, "supply taskId")
	}
	if strings.TrimSpace(p.Kind) == "" {
		return validationError("kind", "kind is required", "non-empty string", nil, "supply kind")
	}
	if strings.TrimSpace(p.AboutPrincipalRef) == "" {
		return validationError("aboutPrincipalRef", "aboutPrincipalRef is required", "principal ref", nil, "supply aboutPrincipalRef")
	}
	if !isCanonicalLedgerWriter(p.WrittenBy) {
		return validationError("principal_ref", "ledger append requires an explicit canonical agent:<id> principal", "agent:<id>", nil, "launch with --principal-ref agent:<id>")
	}
	if len(p.Body) == 0 {
		p.Body = json.RawMessage(`{}`)
	}
	var body map[string]any
	if err := json.Unmarshal(p.Body, &body); err != nil || body == nil {
		return validationError("body", "body must be a JSON object", "JSON object", nil, "supply --body JSON")
	}
	return nil
}

func isCanonicalLedgerWriter(principal string) bool {
	return attribution.ValidatePrincipalRef(principal) == nil
}

// AppendLedger allocates seq and inserts the row in the same BEGIN IMMEDIATE
// transaction.  Completed instances remain admissible by resolving latest,
// rather than active, workflow instances.
func (s *Service) AppendLedger(p AppendLedgerParams) (*LedgerEntry, error) {
	if len(p.Body) == 0 {
		p.Body = json.RawMessage(`{}`)
	}
	if err := validateLedgerAppend(p); err != nil {
		return nil, err
	}
	taskUUID, taskID, err := selectors.ResolveTask(s.db, p.TaskID)
	if err != nil {
		return nil, err
	}
	entry := &LedgerEntry{
		UUID:              uuid.NewString(),
		TaskID:            taskID,
		Kind:              strings.TrimSpace(p.Kind),
		AboutPrincipalRef: strings.TrimSpace(p.AboutPrincipalRef),
		WrittenBy:         strings.TrimSpace(p.WrittenBy),
		Body:              append(json.RawMessage(nil), p.Body...),
	}

	for attempt := 0; attempt < 5; attempt++ {
		err = withImmediateTx(s.db, func(tx *sql.Tx) error {
			var instanceID string
			if err := tx.QueryRow(`SELECT id FROM workflow_instances WHERE task_uuid = ? ORDER BY created_at DESC, id DESC LIMIT 1`, taskUUID).Scan(&instanceID); err != nil {
				if err == sql.ErrNoRows {
					return validationError("taskId", "task has no workflow instance", "task with attached workflow", nil, "attach a workflow before appending ledger entries")
				}
				return err
			}
			var seq int64
			if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM ledger_entry WHERE instance_id = ?`, instanceID).Scan(&seq); err != nil {
				return err
			}
			entry.InstanceID = instanceID
			entry.Seq = seq
			entry.TS = s.now().UTC().Format(time.RFC3339Nano)
			_, err := tx.Exec(`INSERT INTO ledger_entry (uuid, instance_id, task_id, seq, ts, kind, about_principal_ref, written_by, body_json)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, entry.UUID, entry.InstanceID, entry.TaskID, entry.Seq, entry.TS, entry.Kind, entry.AboutPrincipalRef, entry.WrittenBy, string(entry.Body))
			return err
		})
		if err == nil {
			return entry, nil
		}
		if !db.IsBusy(err) && !strings.Contains(err.Error(), "ledger_entry.instance_id, ledger_entry.seq") {
			return nil, err
		}
	}
	return nil, fmt.Errorf("append ledger entry: contention did not resolve: %w", err)
}

func (s *Service) ListLedger(p ListLedgerParams) (LedgerListResult, error) {
	if strings.TrimSpace(p.TaskID) == "" && strings.TrimSpace(p.AboutPrincipalRef) == "" {
		return LedgerListResult{}, validationError("filter", "taskId or aboutPrincipalRef is required", "taskId or aboutPrincipalRef", nil, "supply --task or --principal")
	}
	if p.Limit < 0 {
		return LedgerListResult{}, validationError("limit", "limit must be non-negative", "non-negative integer", nil, "supply a non-negative --limit")
	}
	if p.TaskID != "" && p.Cursor != "" {
		return LedgerListResult{}, validationError("cursor", "cursor is only supported for cross-instance projections", "no cursor with taskId", nil, "remove --cursor or query by --principal")
	}

	if p.TaskID != "" {
		_, taskID, err := selectors.ResolveTask(s.db, p.TaskID)
		if err != nil {
			return LedgerListResult{}, err
		}
		return s.listLedgerForTask(taskID, p)
	}
	return s.listLedgerProjection(p)
}

func (s *Service) listLedgerForTask(taskID string, p ListLedgerParams) (LedgerListResult, error) {
	query := `SELECT seq, uuid, instance_id, task_id, ts, kind, about_principal_ref, written_by, body_json FROM ledger_entry WHERE task_id = ?`
	args := []any{taskID}
	if p.Kind != "" {
		query += ` AND kind = ?`
		args = append(args, p.Kind)
	}
	if p.Since != "" {
		query += ` AND ts >= ?`
		args = append(args, p.Since)
	}
	if p.Until != "" {
		query += ` AND ts <= ?`
		args = append(args, p.Until)
	}
	query += ` ORDER BY instance_id ASC, seq ASC`
	if p.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, p.Limit)
	}
	return scanLedgerEntries(s.db.Query(query, args...))
}

func (s *Service) listLedgerProjection(p ListLedgerParams) (LedgerListResult, error) {
	cur, err := decodeLedgerCursor(p.Cursor)
	if err != nil {
		return LedgerListResult{}, err
	}
	query := `SELECT seq, uuid, instance_id, task_id, ts, kind, about_principal_ref, written_by, body_json FROM ledger_entry WHERE about_principal_ref = ?`
	args := []any{p.AboutPrincipalRef}
	if p.Kind != "" {
		query += ` AND kind = ?`
		args = append(args, p.Kind)
	}
	if p.Since != "" {
		query += ` AND ts >= ?`
		args = append(args, p.Since)
	}
	if p.Until != "" {
		query += ` AND ts <= ?`
		args = append(args, p.Until)
	}
	if cur != nil {
		query += ` AND (ts > ? OR (ts = ? AND instance_id > ?) OR (ts = ? AND instance_id = ? AND seq > ?))`
		args = append(args, cur.TS, cur.TS, cur.InstanceID, cur.TS, cur.InstanceID, cur.Seq)
	}
	query += ` ORDER BY ts ASC, instance_id ASC, seq ASC`
	limit := p.Limit
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit+1)
	}
	result, err := scanLedgerEntries(s.db.Query(query, args...))
	if err != nil || limit == 0 || len(result.Entries) <= limit {
		return result, err
	}
	last := result.Entries[limit-1]
	result.Entries = result.Entries[:limit]
	result.NextCursor, err = encodeLedgerCursor(ledgerCursor{TS: last.TS, InstanceID: last.InstanceID, Seq: last.Seq})
	return result, err
}

func scanLedgerEntries(rows *sql.Rows, err error) (LedgerListResult, error) {
	if err != nil {
		return LedgerListResult{}, err
	}
	defer func() { _ = rows.Close() }()
	result := LedgerListResult{Entries: []LedgerEntry{}}
	for rows.Next() {
		var entry LedgerEntry
		var body string
		if err := rows.Scan(&entry.Seq, &entry.UUID, &entry.InstanceID, &entry.TaskID, &entry.TS, &entry.Kind, &entry.AboutPrincipalRef, &entry.WrittenBy, &body); err != nil {
			return LedgerListResult{}, err
		}
		entry.Body = json.RawMessage(body)
		result.Entries = append(result.Entries, entry)
	}
	return result, rows.Err()
}

func encodeLedgerCursor(cur ledgerCursor) (string, error) {
	b, err := json.Marshal(cur)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeLedgerCursor(value string) (*ledgerCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, validationError("cursor", "invalid ledger cursor", "opaque ledger cursor", nil, "use nextCursor returned by ledger.list")
	}
	var cur ledgerCursor
	if err := json.Unmarshal(b, &cur); err != nil || cur.TS == "" || cur.InstanceID == "" || cur.Seq < 1 {
		return nil, validationError("cursor", "invalid ledger cursor", "opaque ledger cursor", nil, "use nextCursor returned by ledger.list")
	}
	return &cur, nil
}
