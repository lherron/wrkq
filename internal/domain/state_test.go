package domain

import (
	"strings"
	"testing"
)

// TestParseState_Rejects: an unknown or wrong-case state is refused with a
// message that lists every valid state. All invalid inputs take the one
// default branch, so one wrong-case row and the empty row suffice.
//
// rewrite-held (§1): the surface is `wrkq set --state` / wrkq.task.update
// (rpccli, workrpc), where no refusal test exists yet.
func TestParseState_Rejects(t *testing.T) {
	for _, input := range []string{"OPEN", ""} {
		t.Run(input, func(t *testing.T) {
			_, err := ParseState(input)
			if err == nil {
				t.Fatalf("ParseState(%q) expected error, got nil", input)
			}
			want := "must be one of: " + strings.Join(AllStates(), ", ")
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("ParseState(%q) error %q does not list the valid states (%q)", input, err.Error(), want)
			}
		})
	}
}

// TestParseState_AcceptsAll9 verifies all 9 valid states parse successfully,
// the result equals the typed constant, and string(state) round-trips to the wire form.
//
// Target typed constants (bare named string type — NO custom Marshal/Unmarshal):
//
//	StateIdea, StateDraft, StateOpen, StateInProgress, StateCompleted,
//	StateBlocked, StateCancelled, StateArchived, StateDeleted
func TestParseState_AcceptsAll9(t *testing.T) {
	cases := []struct {
		wire string
		want State
	}{
		{"idea", StateIdea},
		{"draft", StateDraft},
		{"open", StateOpen},
		{"in_progress", StateInProgress},
		{"completed", StateCompleted},
		{"blocked", StateBlocked},
		{"cancelled", StateCancelled},
		{"archived", StateArchived},
		{"deleted", StateDeleted},
	}
	for _, tc := range cases {
		t.Run(tc.wire, func(t *testing.T) {
			got, err := ParseState(tc.wire)
			if err != nil {
				t.Fatalf("ParseState(%q) unexpected error: %v", tc.wire, err)
			}
			if got != tc.want {
				t.Errorf("ParseState(%q) = %v, want %v", tc.wire, got, tc.want)
			}
			// String round-trip: the underlying string must equal the wire form.
			// (Bare named type — no String() method required; string() cast suffices.)
			if string(got) != tc.wire {
				t.Errorf("string(ParseState(%q)) = %q, want %q", tc.wire, string(got), tc.wire)
			}
		})
	}
}
