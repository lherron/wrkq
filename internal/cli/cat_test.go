package cli

import "testing"

func TestScopeRefToHandle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"full scopeRef with task", "agent:clod:project:wrkq:task:primary", "clod@wrkq:primary"},
		{"project scopeRef", "agent:cody:project:wrkq", "cody@wrkq"},
		{"agent-only scopeRef", "agent:clod", "clod"},
		{"bare actor slug passthrough", "claude-code-agent", "claude-code-agent"},
		{"empty passthrough", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopeRefToHandle(tc.in); got != tc.want {
				t.Errorf("scopeRefToHandle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
