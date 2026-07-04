package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

type causedByCLIResult struct {
	stdout string
	stderr string
	err    error
}

func runCausedByCLI(t *testing.T, args ...string) causedByCLIResult {
	t.Helper()
	resetCausedByCLIState(t)

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	err := rootCmd.Execute()

	rootCmd.SetArgs(nil)
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	resetCausedByCLIState(t)

	return causedByCLIResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func resetCausedByCLIState(t *testing.T) {
	t.Helper()

	rootOutput = ""
	resetTouchGlobals()
	resetCatGlobals()
	resetFindFlagsForTest(t)

	for _, name := range []string{"db", "as", "project", "output"} {
		flag := rootCmd.PersistentFlags().Lookup(name)
		if flag == nil {
			continue
		}
		if err := rootCmd.PersistentFlags().Set(name, ""); err != nil {
			t.Fatalf("reset persistent flag %s: %v", name, err)
		}
		flag.Changed = false
	}
}

func isolateCausedByCLIConfig(t *testing.T, dbPath, projectRoot string) {
	t.Helper()

	homeDir := t.TempDir()
	runDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(runDir); err != nil {
		t.Fatalf("chdir test run dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldCwd)
	})

	for _, key := range []string{
		"WRKQ_DB",
		"WRKQ_DB_PATH",
		"WRKQ_DB_PATH_FILE",
		"WRKQ_ATTACH_DIR",
		"WRKQ_PROJECT_ROOT",
		"WRKQ_OUTPUT",
		"AGENT_SCOPE_REF",
		"ASP_SCOPE_REF",
		"ASP_HANDLE",
		"ASP_AGENT_ID",
		"ASP_PROJECT",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("PRAESIDIUM_HOME", homeDir)
	t.Setenv("WRKQ_DB_PATH", dbPath)
	t.Setenv("WRKQ_PRINCIPAL_REF", "agent:test-user")
	t.Setenv("WRKQ_PROJECT_ROOT", projectRoot)
}

func mustCausedByCLI(t *testing.T, args ...string) string {
	t.Helper()
	res := runCausedByCLI(t, args...)
	if res.err != nil {
		t.Fatalf("wrkq %v failed: %v\nstdout=%s\nstderr=%s", args, res.err, res.stdout, res.stderr)
	}
	return res.stdout
}

func mustSingleTaskID(t *testing.T, stdout string) string {
	t.Helper()
	var tasks []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(stdout), &tasks); err != nil {
		t.Fatalf("decode task JSON: %v\n%s", err, stdout)
	}
	if len(tasks) != 1 || tasks[0].ID == "" {
		t.Fatalf("expected one task ID, got %+v from %s", tasks, stdout)
	}
	return tasks[0].ID
}

func TestCausedByCLIRoundTripFindAndClear(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	insertContainer(t, database, "00000000-0000-0000-0000-000000000101", "P-00101", "wrkq", "wrkq", "", "2024-01-01T00:00:00Z")
	insertContainer(t, database, "00000000-0000-0000-0000-000000000102", "P-00102", "inbox", "Inbox", "00000000-0000-0000-0000-000000000101", "2024-01-01T00:00:00Z")
	isolateCausedByCLIConfig(t, dbPath, "wrkq")

	producerOne := mustSingleTaskID(t, mustCausedByCLI(t, "touch", "inbox/producer-one", "--json"))
	producerTwo := mustSingleTaskID(t, mustCausedByCLI(t, "touch", "inbox/producer-two", "--json"))

	// Bounded unit bar for T-04229: caused_by is a first-class CLI field, so
	// create must accept ordered duplicate input, cat must project the
	// de-duplicated array, and find must only match the stated cause.
	defect := mustSingleTaskID(t, mustCausedByCLI(t,
		"touch", "inbox/caused-by-defect", "--caused-by", producerOne+","+producerTwo+","+producerOne, "--json",
	))
	guard := mustSingleTaskID(t, mustCausedByCLI(t,
		"touch", "inbox/caused-by-guard", "--caused-by", producerTwo, "--json",
	))

	var catRows []struct {
		ID       string   `json:"id"`
		CausedBy []string `json:"caused_by"`
	}
	if err := json.Unmarshal([]byte(mustCausedByCLI(t, "cat", defect, "--json")), &catRows); err != nil {
		t.Fatalf("decode cat JSON: %v", err)
	}
	if len(catRows) != 1 {
		t.Fatalf("expected one cat row, got %+v", catRows)
	}
	if got, want := catRows[0].CausedBy, []string{producerOne, producerTwo}; !equalStrings(got, want) {
		t.Fatalf("cat caused_by=%v, want %v", got, want)
	}

	var findRows []struct {
		ID       string   `json:"id"`
		CausedBy []string `json:"caused_by"`
	}
	if err := json.Unmarshal([]byte(mustCausedByCLI(t,
		"find", "--caused-by", producerOne, "--type", "t", "--json",
	)), &findRows); err != nil {
		t.Fatalf("decode find JSON: %v", err)
	}
	if got, want := idsFromCausedByFindRows(findRows), []string{defect}; !equalStrings(got, want) {
		t.Fatalf("find --caused-by %s returned ids=%v, want %v; guard task %s must not match across other causes", producerOne, got, want, guard)
	}
	if len(findRows) != 1 || !equalStrings(findRows[0].CausedBy, []string{producerOne, producerTwo}) {
		t.Fatalf("find JSON should include caused_by projection, got %+v", findRows)
	}

	mustCausedByCLI(t, "set", defect, "--caused-by", "")
	if err := json.Unmarshal([]byte(mustCausedByCLI(t, "cat", defect, "--json")), &catRows); err != nil {
		t.Fatalf("decode cleared cat JSON: %v", err)
	}
	if len(catRows) != 1 {
		t.Fatalf("expected one cleared cat row, got %+v", catRows)
	}
	if len(catRows[0].CausedBy) != 0 {
		t.Fatalf("cleared caused_by=%v, want empty", catRows[0].CausedBy)
	}
}

func idsFromCausedByFindRows(rows []struct {
	ID       string   `json:"id"`
	CausedBy []string `json:"caused_by"`
}) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
