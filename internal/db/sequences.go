package db

import (
	"database/sql"
	"fmt"
)

// SequenceSpec defines how to derive the max ID for a friendly-ID table.
type SequenceSpec struct {
	SeqTable    string
	EntityTable string
	IDColumn    string
	Prefix      string
}

// SequenceDrift captures drift between sqlite_sequence and the max existing ID.
type SequenceDrift struct {
	SeqTable    string
	EntityTable string
	MaxID       int
	SeqValue    int
}

type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// DefaultSequenceSpecs returns the built-in friendly-ID sequences.
func DefaultSequenceSpecs() []SequenceSpec {
	return []SequenceSpec{
		{SeqTable: "actor_seq", EntityTable: "actors", IDColumn: "id", Prefix: "A-"},
		{SeqTable: "container_seq", EntityTable: "containers", IDColumn: "id", Prefix: "P-"},
		{SeqTable: "task_seq", EntityTable: "tasks", IDColumn: "id", Prefix: "T-"},
		{SeqTable: "attachment_seq", EntityTable: "attachments", IDColumn: "id", Prefix: "ATT-"},
		{SeqTable: "event_seq", EntityTable: "event_log", IDColumn: "id", Prefix: ""},
	}
}

// SequenceDrifts returns any sequences whose sqlite_sequence value is below the max existing ID.
func SequenceDrifts(exec sqlExecutor, specs []SequenceSpec) ([]SequenceDrift, error) {
	drifts := []SequenceDrift{}

	for _, spec := range specs {
		maxID, err := maxExistingID(exec, spec)
		if err != nil {
			return nil, fmt.Errorf("failed to compute max ID for %s: %w", spec.EntityTable, err)
		}

		seqValue, err := currentSequence(exec, spec.SeqTable)
		if err != nil {
			return nil, fmt.Errorf("failed to read sqlite_sequence for %s: %w", spec.SeqTable, err)
		}

		if seqValue < maxID {
			drifts = append(drifts, SequenceDrift{
				SeqTable:    spec.SeqTable,
				EntityTable: spec.EntityTable,
				MaxID:       maxID,
				SeqValue:    seqValue,
			})
		}
	}

	return drifts, nil
}

// FixSequenceDrifts updates sqlite_sequence to match the max existing IDs.
// Returns the list of sequences that were updated.
func FixSequenceDrifts(exec sqlExecutor, specs []SequenceSpec) ([]SequenceDrift, error) {
	drifts, err := SequenceDrifts(exec, specs)
	if err != nil {
		return nil, err
	}

	for _, drift := range drifts {
		if err := setSequence(exec, drift.SeqTable, drift.MaxID); err != nil {
			return nil, fmt.Errorf("failed to update sqlite_sequence for %s: %w", drift.SeqTable, err)
		}
	}

	return drifts, nil
}

func maxExistingID(exec sqlExecutor, spec SequenceSpec) (int, error) {
	if spec.Prefix == "" {
		query := fmt.Sprintf("SELECT COALESCE(MAX(%s), 0) FROM %s", spec.IDColumn, spec.EntityTable)
		var maxID int
		if err := exec.QueryRow(query).Scan(&maxID); err != nil {
			return 0, err
		}
		return maxID, nil
	}

	startPos := len(spec.Prefix) + 1
	query := fmt.Sprintf(
		"SELECT COALESCE(MAX(CAST(SUBSTR(%s, ?) AS INTEGER)), 0) FROM %s WHERE %s LIKE ?",
		spec.IDColumn, spec.EntityTable, spec.IDColumn,
	)
	var maxID int
	if err := exec.QueryRow(query, startPos, spec.Prefix+"%").Scan(&maxID); err != nil {
		return 0, err
	}
	return maxID, nil
}

func currentSequence(exec sqlExecutor, seqTable string) (int, error) {
	var seq sql.NullInt64
	err := exec.QueryRow("SELECT seq FROM sqlite_sequence WHERE name = ?", seqTable).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return int(seq.Int64), nil
}

func setSequence(exec sqlExecutor, seqTable string, value int) error {
	res, err := exec.Exec("UPDATE sqlite_sequence SET seq = ? WHERE name = ?", value, seqTable)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		return nil
	}
	_, err = exec.Exec("INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)", seqTable, value)
	return err
}
