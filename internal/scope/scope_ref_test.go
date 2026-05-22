package scope

import (
	"strings"
	"testing"
)

// Ported from agent-spaces/packages/agent-scope/src/__tests__/scope-ref.test.ts.

func TestTokenGrammar_Valid(t *testing.T) {
	long := strings.Repeat("a", 64)
	valid := []string{
		"alice", "bob-42", "my.agent", "under_score", "A", long,
		"Mix.Ed-Case_123", "0", "0.1.2",
	}
	for _, tok := range valid {
		tok := tok
		t.Run("valid_"+tok, func(t *testing.T) {
			r := ValidateScopeRef("agent:" + tok)
			if !r.OK {
				t.Fatalf("expected token %q to validate; error=%q", tok, r.Error)
			}
		})
	}
}

func TestTokenGrammar_Invalid(t *testing.T) {
	tooLong := strings.Repeat("a", 65)
	invalid := []string{
		"",             // empty
		tooLong,        // too long
		"has space",    // space
		"has/slash",    // slash
		"has:colon",    // colon (delimiter, not token)
		"has@at",       // at sign
		"emoji\U0001F600",
	}
	for _, tok := range invalid {
		tok := tok
		t.Run("invalid_"+tok, func(t *testing.T) {
			r := ValidateScopeRef("agent:" + tok)
			if r.OK {
				t.Fatalf("expected token %q to be invalid, but it validated", tok)
			}
		})
	}
}

func TestValidScopeRefForms(t *testing.T) {
	type tc struct {
		input     string
		kind      ScopeKind
		agentID   string
		projectID string
		taskID    string
		roleName  string
	}
	cases := []tc{
		{"agent:alice", KindAgent, "alice", "", "", ""},
		{"agent:alice:project:demo", KindProject, "alice", "demo", "", ""},
		{"agent:alice:project:demo:role:tester", KindProjectRole, "alice", "demo", "", "tester"},
		{"agent:alice:project:demo:task:t1", KindProjectTask, "alice", "demo", "t1", ""},
		{"agent:alice:project:demo:task:t1:role:tester", KindProjectTaskRole, "alice", "demo", "t1", "tester"},
	}
	for _, c := range cases {
		c := c
		t.Run("parses_"+c.input, func(t *testing.T) {
			parsed, err := ParseScopeRef(c.input)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if parsed.Kind != c.kind {
				t.Errorf("kind=%q, want %q", parsed.Kind, c.kind)
			}
			if parsed.AgentID != c.agentID {
				t.Errorf("AgentID=%q, want %q", parsed.AgentID, c.agentID)
			}
			if parsed.ProjectID != c.projectID {
				t.Errorf("ProjectID=%q, want %q", parsed.ProjectID, c.projectID)
			}
			if parsed.TaskID != c.taskID {
				t.Errorf("TaskID=%q, want %q", parsed.TaskID, c.taskID)
			}
			if parsed.RoleName != c.roleName {
				t.Errorf("RoleName=%q, want %q", parsed.RoleName, c.roleName)
			}
			if parsed.ScopeRef != c.input {
				t.Errorf("ScopeRef=%q, want %q", parsed.ScopeRef, c.input)
			}
		})

		t.Run("roundtrips_"+c.input, func(t *testing.T) {
			parsed, err := ParseScopeRef(c.input)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if got := FormatScopeRef(parsed); got != c.input {
				t.Errorf("FormatScopeRef=%q, want %q", got, c.input)
			}
		})

		t.Run("validates_"+c.input, func(t *testing.T) {
			r := ValidateScopeRef(c.input)
			if !r.OK {
				t.Errorf("ValidateScopeRef(%q).OK=false; error=%q", c.input, r.Error)
			}
		})
	}
}

func TestInvalidScopeRefForms(t *testing.T) {
	cases := []struct {
		input, reason string
	}{
		{"project:demo", "project without agent prefix"},
		{"agent:alice:session:s1", "embedded sessionId"},
		{"agent:alice:task:t1", "task without project"},
		{"agent:alice:role:tester", "role without project"},
		{"", "empty string"},
		{"not-a-scope-ref", "missing agent: prefix"},
		{"agent::alice", "double colon (empty token)"},
		{"agent:alice:", "trailing colon"},
		{"agent:alice:project:demo:channel:general", "unknown segment type"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.reason, func(t *testing.T) {
			r := ValidateScopeRef(c.input)
			if r.OK {
				t.Errorf("expected %q invalid (%s)", c.input, c.reason)
			}
			if r.Error == "" {
				t.Errorf("expected non-empty error for %q", c.input)
			}
		})
	}

	t.Run("ParseScopeRef returns error on invalid input", func(t *testing.T) {
		_, err := ParseScopeRef("project:demo")
		if err == nil {
			t.Errorf("expected error for invalid scopeRef")
		}
	})
}

func TestAncestorScopeRefs(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"agent:alice", []string{"agent:alice"}},
		{"agent:alice:project:demo", []string{"agent:alice", "agent:alice:project:demo"}},
		{"agent:alice:project:demo:role:tester", []string{
			"agent:alice",
			"agent:alice:project:demo",
			"agent:alice:project:demo:role:tester",
		}},
		{"agent:alice:project:demo:task:t1", []string{
			"agent:alice",
			"agent:alice:project:demo",
			"agent:alice:project:demo:task:t1",
		}},
		{"agent:alice:project:demo:task:t1:role:tester", []string{
			"agent:alice",
			"agent:alice:project:demo",
			"agent:alice:project:demo:task:t1",
			"agent:alice:project:demo:task:t1:role:tester",
		}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.input, func(t *testing.T) {
			got, err := AncestorScopeRefs(c.input)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if !eqStrings(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}

	t.Run("least to most specific ordering", func(t *testing.T) {
		anc, err := AncestorScopeRefs("agent:alice:project:demo:task:t1")
		if err != nil {
			t.Fatal(err)
		}
		if anc[0] != "agent:alice" {
			t.Errorf("first=%q, want agent:alice", anc[0])
		}
		last := anc[len(anc)-1]
		if last != "agent:alice:project:demo:task:t1" {
			t.Errorf("last=%q, want agent:alice:project:demo:task:t1", last)
		}
	})
}

func TestScopeKinds(t *testing.T) {
	kinds := []ScopeKind{
		KindAgent, KindProject, KindProjectRole, KindProjectTask, KindProjectTaskRole,
	}
	if len(kinds) != 5 {
		t.Errorf("expected 5 ScopeKinds")
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
