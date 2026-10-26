package rpccli

import "testing"

// A `wrkp just` fact is attributed to the task named in the runtime scope ref:
// a named subtask keeps its suffix, a lane suffix (/reviewer) is stripped, and
// a scope with no task — or a token that is not a wrkq task id (XT-, six
// digits) — attributes to none. The task-token grammar itself is
// id.TestFindTaskIDs's; these rows are the scope parsing only.
//
// Held at the inner seam: `wrkp just` has no surface harness yet (it shells out
// to just). Rewrite through `wrkp just <recipe>` + `wrkp log --json` once one
// exists.
func TestWrkpJustAttributesTheScopeRefTaskStrippingTheLane(t *testing.T) {
	for _, tt := range []struct{ scope, want string }{
		{"agent:cody:project:wrkq:task:T-12345.render-preview", "T-12345.render-preview"},
		{"agent:cody:project:wrkq:task:T-12345.render-preview/reviewer", "T-12345.render-preview"},
		{"agent:cody:project:wrkq:task:T-12345/reviewer", "T-12345"},
		{"agent:cody:project:wrkq", ""},
		{"agent:cody:project:wrkq:task:XT-12345", ""},
		{"agent:cody:project:wrkq:task:T-123456", ""},
	} {
		if got := wrkpJustTaskID(tt.scope); got != tt.want {
			t.Errorf("wrkpJustTaskID(%q) = %q, want %q", tt.scope, got, tt.want)
		}
	}
}
