package id

import (
	"reflect"
	"testing"
)

func TestWorkstreamIdentity(t *testing.T) {
	typ, n, err := Parse("W-13301")
	if err != nil || typ != TypeTask || n != 13301 {
		t.Fatalf("parse: %s %d %v", typ, n, err)
	}
	owner, slug, err := ParseTask("W-13301")
	if err != nil || owner != "W-13301" || slug != "" || !IsTask(owner) || !IsFriendlyID(owner) {
		t.Fatalf("task: %s %s %v", owner, slug, err)
	}
	for _, invalid := range []string{"W-13301.x", "W-1330", "W-133001"} {
		if IsTask(invalid) {
			t.Errorf("accepted %s", invalid)
		}
	}
	if got := FindTaskIDs("W-13301 and T-13302.thread, W-13301."); !reflect.DeepEqual(got, []string{"W-13301", "T-13302.thread", "W-13301"}) {
		t.Fatal(got)
	}
	if FormatTask(13301) != "T-13301" {
		t.Fatal("task formatter changed")
	}
}
