//go:build wrkq_local

package store

import (
	"strings"
	"testing"
)

func TestEnvelopeListIDQueryUsesUniqueIndexes(t *testing.T) {
	database, _ := newMemberPageStore(t)
	after := int64(0)
	for _, ids := range [][]string{nil, {"EN-00001", "envelope-uuid"}} {
		query, args, err := envelopeListQuery(EnvelopeListParams{IDs: ids, AfterMessageSeq: &after, Limit: 501})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		plan := strings.Join(details, "\n")
		if ids == nil {
			if !strings.Contains(plan, "envelopes_message_seq_idx") {
				t.Fatalf("unbounded sequence scan lost index: %s", plan)
			}
		} else if strings.Contains(plan, "envelopes_message_seq_idx") || !strings.Contains(plan, "MULTI-INDEX OR") {
			t.Fatalf("id replay must use id/uuid lookups, not ledger scan: %s", plan)
		}
	}
}
