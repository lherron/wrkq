package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
)

// CollaborationCursorInvalidError fences a follow-up page from a replacement
// collaboration ledger. No rows from the new incarnation accompany this error.
type CollaborationCursorInvalidError struct {
	Expected string
	Current  string
}

func (e *CollaborationCursorInvalidError) Error() string {
	return "cursor_invalid: collaboration ledger incarnation changed"
}

var envelopePageColumns = qualifySQLColumns(envelopeColumns, "e")

// memberEnvelopeSeqSQL selects, in sequence order, the envelopes in rooms the
// exact member+principal currently belongs to on one side of a sequence
// boundary. The sequence index drives the outer loop of the CROSS JOIN.
func memberEnvelopeSeqSQL(selectList, boundaryOp, order, limit string) string {
	return `SELECT ` + selectList + `
	FROM envelopes e INDEXED BY envelopes_message_seq_idx
	CROSS JOIN room_members m INDEXED BY room_members_observation_idx
	WHERE CAST(SUBSTR(e.id, 4) AS INTEGER) ` + boundaryOp + ` ?
	  AND m.member_ref = ?
	  AND m.member_principal_ref = ?
	  AND m.left_at IS NULL
	  AND m.room_uuid = e.room_uuid
	ORDER BY CAST(SUBSTR(e.id, 4) AS INTEGER) ` + order + `
	LIMIT ` + limit
}

var (
	memberEnvelopeRowSQL          = `CAST(SUBSTR(e.id, 4) AS INTEGER), ` + envelopePageColumns
	memberEnvelopePageBeforeSQL   = memberEnvelopeSeqSQL(memberEnvelopeRowSQL, "<", "DESC", "?")
	memberEnvelopePageAfterSQL    = memberEnvelopeSeqSQL(memberEnvelopeRowSQL, ">", "ASC", "?")
	memberEnvelopeExistsBeforeSQL = memberEnvelopeSeqSQL("1", "<=", "DESC", "1")
	memberEnvelopeExistsAfterSQL  = memberEnvelopeSeqSQL("1", ">=", "ASC", "1")
)

// MemberEnvelopePageParams selects one bounded direction over the global
// collaboration envelope sequence. The API validates cursor shape and limit;
// the store accepts the exact normalized member address + principal pair.
type MemberEnvelopePageParams struct {
	MemberRef                 string
	MemberPrincipalRef        string
	BeforeMessageSeq          *int64
	AfterMessageSeq           *int64
	Limit                     int
	ExpectedLedgerIncarnation string
}

// MemberEnvelopePageItem couples the immutable ledger sequence to its envelope.
type MemberEnvelopePageItem struct {
	MessageSeq int64
	Envelope   domain.Envelope
}

// MemberEnvelopePage is transactionally consistent repository output.
// MaterializedRows is internal query-bound evidence: it includes the one
// lookahead row and never exceeds Limit+1.
type MemberEnvelopePage struct {
	LedgerIncarnation string
	HeadMessageSeq    int64
	HasMoreBefore     bool
	HasMoreAfter      bool
	Items             []MemberEnvelopePageItem
	MaterializedRows  int
}

// MemberEnvelopePage reads incarnation, head, availability, and at most
// limit+1 exact-member rows in one read transaction. The sequence index is the
// outer CROSS JOIN loop, so ordering is global across rooms and unrelated room
// count cannot enter the admitted result set.
func (rs *RoomStore) MemberEnvelopePage(ctx context.Context, params MemberEnvelopePageParams) (*MemberEnvelopePage, error) {
	if (params.BeforeMessageSeq == nil) == (params.AfterMessageSeq == nil) {
		return nil, fmt.Errorf("member envelope page requires exactly one cursor direction")
	}
	if params.Limit < 1 || params.Limit > 500 {
		return nil, fmt.Errorf("member envelope page limit must be between 1 and 500")
	}
	if strings.TrimSpace(params.MemberRef) == "" || strings.TrimSpace(params.MemberPrincipalRef) == "" {
		return nil, fmt.Errorf("member envelope page requires exact member and principal refs")
	}
	conn, err := rs.store.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire collaboration page connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN DEFERRED"); err != nil {
		return nil, fmt.Errorf("failed to begin collaboration page transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	result := &MemberEnvelopePage{Items: []MemberEnvelopePageItem{}}
	if err := conn.QueryRowContext(ctx,
		"SELECT incarnation FROM collaboration_ledger_meta WHERE singleton = 1").
		Scan(&result.LedgerIncarnation); err != nil {
		return nil, fmt.Errorf("failed to read collaboration ledger incarnation: %w", err)
	}
	if params.ExpectedLedgerIncarnation != "" && params.ExpectedLedgerIncarnation != result.LedgerIncarnation {
		return nil, &CollaborationCursorInvalidError{
			Expected: params.ExpectedLedgerIncarnation,
			Current:  result.LedgerIncarnation,
		}
	}
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM envelope_seq").
		Scan(&result.HeadMessageSeq); err != nil {
		return nil, fmt.Errorf("failed to read collaboration ledger head: %w", err)
	}

	query := memberEnvelopePageBeforeSQL
	var boundary int64
	if params.AfterMessageSeq != nil {
		query = memberEnvelopePageAfterSQL
		boundary = *params.AfterMessageSeq
	} else {
		boundary = *params.BeforeMessageSeq
	}
	rows, err := conn.QueryContext(ctx, query, boundary, params.MemberRef,
		params.MemberPrincipalRef, params.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("failed to query member envelope page: %w", err)
	}
	for rows.Next() {
		item := MemberEnvelopePageItem{}
		destinations := append([]interface{}{&item.MessageSeq}, envelopeScanDestinations(&item.Envelope)...)
		if err := rows.Scan(destinations...); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to scan member envelope page: %w", err)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("failed to iterate member envelope page: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close member envelope page: %w", err)
	}
	result.MaterializedRows = len(result.Items)

	if params.BeforeMessageSeq != nil {
		result.HasMoreBefore = len(result.Items) > params.Limit
		if result.HasMoreBefore {
			result.Items = result.Items[:params.Limit]
		}
		for left, right := 0, len(result.Items)-1; left < right; left, right = left+1, right-1 {
			result.Items[left], result.Items[right] = result.Items[right], result.Items[left]
		}
		result.HasMoreAfter, err = memberEnvelopeExists(ctx, conn, memberEnvelopeExistsAfterSQL,
			boundary, params.MemberRef, params.MemberPrincipalRef)
	} else {
		result.HasMoreAfter = len(result.Items) > params.Limit
		if result.HasMoreAfter {
			result.Items = result.Items[:params.Limit]
		}
		result.HasMoreBefore, err = memberEnvelopeExists(ctx, conn, memberEnvelopeExistsBeforeSQL,
			boundary, params.MemberRef, params.MemberPrincipalRef)
	}
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, fmt.Errorf("failed to commit collaboration page transaction: %w", err)
	}
	committed = true
	return result, nil
}

type contextQueryRower interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func memberEnvelopeExists(ctx context.Context, queryer contextQueryRower, query string, boundary int64, memberRef, principalRef string) (bool, error) {
	var one int
	err := queryer.QueryRowContext(ctx, query, boundary, memberRef, principalRef).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to query member envelope availability: %w", err)
	}
	return true, nil
}
