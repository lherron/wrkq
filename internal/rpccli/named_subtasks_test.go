package rpccli

import (
	"reflect"
	"strings"
	"testing"
)

func TestNamedSubtaskAttributionMatchers(t *testing.T) {
	for _, tt := range []struct{ token, want string }{
		{"T-12345.render-preview", "T-12345.render-preview"},
		{"T-12345.a", "T-12345.a"},
		{"T-12345", "T-12345"},
		{"T-12345.", "T-12345"},
		{"T-12345.Next", "T-12345"},
		{"T-12345." + strings.Repeat("a", 57), ""},
	} {
		t.Run(tt.token, func(t *testing.T) {
			want := []string{}
			if tt.want != "" {
				want = append(want, tt.want)
			}
			if got := wrkpGitTaskIDs("fix: " + tt.token + "\n\n" + tt.token); !reflect.DeepEqual(got, want) {
				t.Errorf("commit tasks = %v, want %v", got, want)
			}
			for _, suffix := range []string{"", "/reviewer"} {
				if got := wrkpJustTaskID("agent:cody:project:wrkq:task:" + tt.token + suffix); got != tt.want {
					t.Errorf("scope task = %q, want %q", got, tt.want)
				}
			}
		})
	}
	for _, scope := range []string{"agent:cody:project:wrkq", "agent:cody:project:wrkq:task:XT-12345", "agent:cody:project:wrkq:task:T-123456"} {
		if got := wrkpJustTaskID(scope); got != "" {
			t.Errorf("scope %q yielded %q", scope, got)
		}
	}
}
