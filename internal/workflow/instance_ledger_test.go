package workflow

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func ledgerFixture(t *testing.T) (*Service, string) {
	t.Helper()
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	return svc, taskUUID
}

func TestLedgerAppendIsAppendOnlyAndStampsInstanceAttribution(t *testing.T) {
	svc, task := ledgerFixture(t)
	entry, err := svc.AppendLedger(AppendLedgerParams{
		TaskID: task, Kind: "runner_start", AboutPrincipalRef: "agent:subject", WrittenBy: "agent:writer",
		Body: []byte(`{"refs":{"logs":{"narration":"/tmp/narration.log"}},"workflowOnly":{"row":5}}`),
	})
	if err != nil {
		t.Fatalf("AppendLedger: %v", err)
	}
	if entry.Seq != 1 || entry.InstanceID == "" || entry.TaskID == "" || entry.WrittenBy != "agent:writer" {
		t.Fatalf("entry = %+v", entry)
	}
	if _, err := svc.db.Exec(`UPDATE ledger_entry SET kind = 'mutated' WHERE uuid = ?`, entry.UUID); err == nil {
		t.Fatal("UPDATE ledger_entry unexpectedly succeeded")
	}
	if _, err := svc.db.Exec(`DELETE FROM ledger_entry WHERE uuid = ?`, entry.UUID); err == nil {
		t.Fatal("DELETE ledger_entry unexpectedly succeeded")
	}
}

func TestLedgerAppendAdmissionAndSettledInstance(t *testing.T) {
	svc, task := ledgerFixture(t)
	for _, writer := range []string{"", "system:wrkf", "human:lance", "agent:cody:project:wrkq"} {
		if _, err := svc.AppendLedger(AppendLedgerParams{TaskID: task, Kind: "strike", AboutPrincipalRef: "agent:about", WrittenBy: writer}); err == nil {
			t.Errorf("writer %q unexpectedly admitted", writer)
		}
	}
	inst, err := svc.LatestInstance(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE workflow_instances SET status = 'closed', closed_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), inst.ID); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"reap", "escalation", "close_scorecard"} {
		entry, err := svc.AppendLedger(AppendLedgerParams{TaskID: task, Kind: kind, AboutPrincipalRef: "agent:about", WrittenBy: "agent:cody"})
		if err != nil {
			t.Fatalf("settled append %s: %v", kind, err)
		}
		if entry.InstanceID != inst.ID {
			t.Fatalf("settled append instance = %q, want %q", entry.InstanceID, inst.ID)
		}
	}
}

func TestLedgerConcurrentAppendProducesContiguousSequence(t *testing.T) {
	svc, task := ledgerFixture(t)
	const writers = 12
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.AppendLedger(AppendLedgerParams{TaskID: task, Kind: "strike", AboutPrincipalRef: fmt.Sprintf("agent:%d", i), WrittenBy: "agent:cody"})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}
	entries, err := svc.ListLedger(ListLedgerParams{TaskID: task})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries.Entries) != writers {
		t.Fatalf("entry count = %d, want %d", len(entries.Entries), writers)
	}
	seq := make([]int64, 0, writers)
	for _, entry := range entries.Entries {
		seq = append(seq, entry.Seq)
	}
	sort.Slice(seq, func(i, j int) bool { return seq[i] < seq[j] })
	for i, got := range seq {
		if want := int64(i + 1); got != want {
			t.Fatalf("seq[%d] = %d, want %d (all=%v)", i, got, want, seq)
		}
	}
}

func TestLedgerProjectionPaginatesEqualTimestampWithoutGaps(t *testing.T) {
	svc, taskOne := ledgerFixture(t)
	projectUUID := ""
	if err := svc.db.QueryRow(`SELECT project_uuid FROM tasks WHERE uuid = ?`, taskOne).Scan(&projectUUID); err != nil {
		t.Fatal(err)
	}
	taskTwo := "ffffffff-ffff-4fff-ffff-000000000002"
	if _, err := svc.db.Exec(`INSERT INTO tasks (uuid, slug, title, specification, project_uuid, state, priority, kind)
		VALUES (?, 'ledger-two', 'Ledger Two', 'spec', ?, 'open', 2, 'task')`, taskTwo, projectUUID); err != nil {
		t.Fatal(err)
	}
	attachSimpleTaskV2(t, svc, taskTwo)
	fixed := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }
	for _, task := range []string{taskOne, taskTwo} {
		for i := 0; i < 2; i++ {
			if _, err := svc.AppendLedger(AppendLedgerParams{TaskID: task, Kind: "strike", AboutPrincipalRef: "agent:larry", WrittenBy: "agent:cody"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	first, err := svc.ListLedger(ListLedgerParams{AboutPrincipalRef: "agent:larry", Kind: "strike", Limit: 2})
	if err != nil || len(first.Entries) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, err=%v", first, err)
	}
	second, err := svc.ListLedger(ListLedgerParams{AboutPrincipalRef: "agent:larry", Kind: "strike", Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Entries) != 2 || second.NextCursor != "" {
		t.Fatalf("second page = %+v, err=%v", second, err)
	}
	seen := map[string]bool{}
	for _, entry := range append(first.Entries, second.Entries...) {
		if seen[entry.UUID] {
			t.Fatalf("duplicate entry %s across pages", entry.UUID)
		}
		seen[entry.UUID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("gap across pages: got %d unique entries", len(seen))
	}
}
