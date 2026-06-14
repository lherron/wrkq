package domain

import (
	"encoding/json"
	"testing"
)

// TestParseState_Rejects verifies that ParseState returns an error for invalid inputs.
// Target API: ParseState(string) (State, error)
// A State value can only be constructed from a recognised wire string.
func TestParseState_Rejects(t *testing.T) {
	invalids := []string{
		"bad",
		"",
		"OPEN",
		"In_Progress",
		"IN_PROGRESS",
		"COMPLETED",
		"random",
		"in progress", // space instead of underscore
		"delete",      // close but wrong
	}
	for _, input := range invalids {
		t.Run(input, func(t *testing.T) {
			_, err := ParseState(input)
			if err == nil {
				t.Errorf("ParseState(%q) expected error, got nil", input)
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

// TestStateJSONRoundTrip verifies that a domain.Task with State=StateOpen marshals
// to JSON with "state":"open" byte-identically (no wire change), and unmarshals back
// to StateOpen with no custom marshaling needed.
//
// Daedalus requirement (3): JSON wire format unchanged.
// Daedalus requirement (B): bare named type — NO custom Marshal/Unmarshal.
//
// This test ALSO proves that domain.Task.State has been migrated to domain.State:
// assigning StateOpen to a `string` field is a compile error in Go.
func TestStateJSONRoundTrip(t *testing.T) {
	// Compile proof: Task.State must be domain.State (not string).
	// If Task.State is still `string`, `State: StateOpen` won't compile.
	task := Task{
		State: StateOpen,
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Wire format must contain exactly "state":"open" as a JSON string.
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal into map failed: %v", err)
	}
	stateVal, ok := m["state"]
	if !ok {
		t.Fatal("JSON output missing 'state' key")
	}
	if stateVal != "open" {
		t.Errorf("JSON state = %v (%T), want string \"open\" (wire must be unchanged)", stateVal, stateVal)
	}

	// Round-trip: unmarshal back to Task and verify StateOpen is recovered.
	var task2 Task
	if err := json.Unmarshal(data, &task2); err != nil {
		t.Fatalf("json.Unmarshal into Task failed: %v", err)
	}
	if task2.State != StateOpen {
		t.Errorf("round-trip state = %v, want StateOpen (%v)", task2.State, StateOpen)
	}
}
