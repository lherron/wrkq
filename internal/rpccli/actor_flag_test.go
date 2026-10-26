//go:build wrkq_local

package rpccli

import (
	"bytes"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func clearActorFlagEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"WRKQ_PRINCIPAL_REF",
		"AGENT_SCOPE_REF",
		"ASP_SCOPE_REF",
		"ASP_HANDLE",
		"ASP_AGENT_ID",
		"ASP_PROJECT",
	} {
		t.Setenv(key, "")
	}
}

// A write with no --as/--principal-ref is attributed to the agent named by the
// runtime AGENT_SCOPE_REF; an explicit --as outranks it.
func TestWriteWithoutPrincipalFlagsAttributesToAgentScopeRefAgent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  string
	}{
		{name: "AGENT_SCOPE_REF alone names the agent", want: "agent:cody"},
		{name: "explicit --as outranks AGENT_SCOPE_REF", flags: []string{"--as", "agent:flag-principal"}, want: "agent:flag-principal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, _ := migratedDBWithTask(t)
			clearActorFlagEnv(t)
			t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:rpccli-test-proj:task:T-05423")

			cmd := NewRootCmdFor("wrkq")
			args := append([]string{"--db", dbPath, "--project", "rpccli-test-proj"}, tc.flags...)
			cmd.SetArgs(append(args, "touch", "attributed", "-t", "Attributed", "--json"))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("touch: %v\n%s", err, out.String())
			}

			database, err := db.Open(dbPath)
			if err != nil {
				t.Fatalf("db.Open: %v", err)
			}
			defer func() { _ = database.Close() }()
			var created string
			if err := database.QueryRow(
				`SELECT COALESCE(created_by_principal_ref, '') FROM tasks WHERE slug = 'attributed'`,
			).Scan(&created); err != nil {
				t.Fatalf("read attribution: %v", err)
			}
			if created != tc.want {
				t.Fatalf("created_by_principal_ref = %q, want %q", created, tc.want)
			}
		})
	}
}
