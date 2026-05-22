package scope

import "testing"

// Ported from agent-spaces/packages/agent-scope/src/__tests__/scope-handle.test.ts.

func TestParseScopeHandle(t *testing.T) {
	type tc struct {
		handle   string
		kind     ScopeKind
		agentID  string
		projID   string
		taskID   string
		roleName string
		scopeRef string
	}
	cases := []tc{
		{"alice", KindAgent, "alice", "", "", "", "agent:alice"},
		{"alice@demo", KindProject, "alice", "demo", "", "", "agent:alice:project:demo"},
		{"alice@demo:t1", KindProjectTask, "alice", "demo", "t1", "", "agent:alice:project:demo:task:t1"},
		{"alice@demo/reviewer", KindProjectRole, "alice", "demo", "", "reviewer", "agent:alice:project:demo:role:reviewer"},
		{"alice@demo:t1/reviewer", KindProjectTaskRole, "alice", "demo", "t1", "reviewer", "agent:alice:project:demo:task:t1:role:reviewer"},
	}
	for _, c := range cases {
		c := c
		t.Run("parses_"+c.handle, func(t *testing.T) {
			p, err := ParseScopeHandle(c.handle)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if p.Kind != c.kind {
				t.Errorf("kind=%q, want %q", p.Kind, c.kind)
			}
			if p.AgentID != c.agentID {
				t.Errorf("AgentID=%q, want %q", p.AgentID, c.agentID)
			}
			if p.ProjectID != c.projID {
				t.Errorf("ProjectID=%q, want %q", p.ProjectID, c.projID)
			}
			if p.TaskID != c.taskID {
				t.Errorf("TaskID=%q, want %q", p.TaskID, c.taskID)
			}
			if p.RoleName != c.roleName {
				t.Errorf("RoleName=%q, want %q", p.RoleName, c.roleName)
			}
			if p.ScopeRef != c.scopeRef {
				t.Errorf("ScopeRef=%q, want %q", p.ScopeRef, c.scopeRef)
			}
		})
	}

	t.Run("dots/hyphens/underscores", func(t *testing.T) {
		p, err := ParseScopeHandle("my.agent@my-project:task_1/role.name")
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if p.Kind != KindProjectTaskRole {
			t.Errorf("kind=%q", p.Kind)
		}
		if p.AgentID != "my.agent" || p.ProjectID != "my-project" || p.TaskID != "task_1" || p.RoleName != "role.name" {
			t.Errorf("unexpected parse: %+v", p)
		}
		want := "agent:my.agent:project:my-project:task:task_1:role:role.name"
		if p.ScopeRef != want {
			t.Errorf("ScopeRef=%q, want %q", p.ScopeRef, want)
		}
	})
}

func TestParseScopeHandle_Invalid(t *testing.T) {
	cases := []struct {
		handle, reason string
	}{
		{"", "empty string"},
		{"@", "bare @"},
		{"@demo", "missing agent (starts with @)"},
		{":t1", "missing agent (starts with :)"},
		{"/role", "missing agent (starts with /)"},
		{"alice@", "trailing @"},
		{"alice@demo:", "trailing :"},
		{"alice@demo/", "trailing /"},
		{"alice@demo:t1!", "invalid character !"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.reason, func(t *testing.T) {
			_, err := ParseScopeHandle(c.handle)
			if err == nil {
				t.Errorf("expected error for %q (%s)", c.handle, c.reason)
			}
		})
	}
}

func TestFormatScopeHandle(t *testing.T) {
	cases := []struct {
		p    ParsedScopeRef
		want string
	}{
		{ParsedScopeRef{Kind: KindAgent, AgentID: "alice", ScopeRef: "agent:alice"}, "alice"},
		{ParsedScopeRef{Kind: KindProject, AgentID: "alice", ProjectID: "demo", ScopeRef: "agent:alice:project:demo"}, "alice@demo"},
		{ParsedScopeRef{Kind: KindProjectTask, AgentID: "alice", ProjectID: "demo", TaskID: "t1", ScopeRef: "agent:alice:project:demo:task:t1"}, "alice@demo:t1"},
		{ParsedScopeRef{Kind: KindProjectRole, AgentID: "alice", ProjectID: "demo", RoleName: "reviewer", ScopeRef: "agent:alice:project:demo:role:reviewer"}, "alice@demo/reviewer"},
		{ParsedScopeRef{Kind: KindProjectTaskRole, AgentID: "alice", ProjectID: "demo", TaskID: "t1", RoleName: "reviewer", ScopeRef: "agent:alice:project:demo:task:t1:role:reviewer"}, "alice@demo:t1/reviewer"},
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.p.Kind), func(t *testing.T) {
			if got := FormatScopeHandle(c.p); got != c.want {
				t.Errorf("FormatScopeHandle=%q, want %q", got, c.want)
			}
		})
	}
}

func TestScopeHandle_RoundTrip(t *testing.T) {
	handles := []string{
		"alice",
		"alice@demo",
		"alice@demo:t1",
		"alice@demo/reviewer",
		"alice@demo:t1/reviewer",
		"my.agent@my-project:task_1/role.name",
	}
	for _, h := range handles {
		h := h
		t.Run(h, func(t *testing.T) {
			p, err := ParseScopeHandle(h)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := FormatScopeHandle(p); got != h {
				t.Errorf("round-trip got %q, want %q", got, h)
			}
		})
	}
}

func TestValidateScopeHandle(t *testing.T) {
	valid := []string{
		"alice",
		"alice@demo",
		"alice@demo:t1",
		"alice@demo/reviewer",
		"alice@demo:t1/reviewer",
		"my.agent@my-project:task_1/role.name",
	}
	for _, h := range valid {
		h := h
		t.Run("valid_"+h, func(t *testing.T) {
			if r := ValidateScopeHandle(h); !r.OK {
				t.Errorf("expected %q valid; error=%q", h, r.Error)
			}
		})
	}

	invalid := []struct{ h, reason string }{
		{"", "empty"},
		{"@", "bare @"},
		{"@demo", "no agent"},
		{"alice@", "trailing @"},
		{"alice@demo:", "trailing :"},
		{"alice@demo/", "trailing /"},
		{"alice@demo:t1!", "invalid char"},
	}
	for _, c := range invalid {
		c := c
		t.Run("invalid_"+c.reason, func(t *testing.T) {
			r := ValidateScopeHandle(c.h)
			if r.OK {
				t.Errorf("expected %q invalid (%s)", c.h, c.reason)
			}
			if r.Error == "" {
				t.Errorf("expected non-empty error for %q", c.h)
			}
		})
	}
}
