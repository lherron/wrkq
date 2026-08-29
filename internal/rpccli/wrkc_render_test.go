package rpccli

import (
	"strings"
	"testing"
)

func TestWrkcRoomTableRendersPairIdentityAndRuneClippedLastLine(t *testing.T) {
	long := strings.Repeat("界", 81) + " never shown"
	rooms := []roomWire{
		{Key: "R-00013", Kind: "adhoc", Work: "open", Activity: "active", MemberCount: 2, MessageCount: 3, LastActivityAt: "2026-08-29T12:00:00Z"},
		{Key: "T-07699", Kind: "task", Work: "open", Activity: "active", MemberCount: 4, MessageCount: 5, LastActivityAt: "2026-08-29T11:00:00Z"},
	}
	identities := map[string]wrkcAdhocIdentity{
		"R-00013": {Members: []string{"cody@wrkq:primary", "mable@wrkq:primary"}, Last: clipWrkcFirstLine(long+"\nsecond line", 80)},
	}

	headers, rows := wrkcRoomTable(rooms, identities)
	if got, want := strings.Join(headers, "|"), "Room|Kind|Work|Activity|Members|Messages|Last activity|Last"; got != want {
		t.Fatalf("headers = %q, want %q", got, want)
	}
	if got, want := rows[0][4], "cody@wrkq:primary, mable@wrkq:primary"; got != want {
		t.Fatalf("pair members = %q, want %q", got, want)
	}
	if got := []rune(rows[0][7]); len(got) != 80 || strings.Contains(rows[0][7], "never shown") {
		t.Fatalf("last preview = %q (%d runes), want an 80-rune first-line clip", rows[0][7], len(got))
	}
	if got, want := rows[1][4], "4"; got != want {
		t.Fatalf("work-room member count = %q, want %q", got, want)
	}
	if rows[1][7] != "" {
		t.Fatalf("work-room Last = %q, want unchanged blank presentation", rows[1][7])
	}
}

func TestWrkcAdhocHeadersUseMembersInsteadOfSubject(t *testing.T) {
	room := roomWire{Key: "R-00025", Kind: "adhoc", Work: "open", Activity: "active", MemberCount: 2}
	identity := &wrkcAdhocIdentity{Members: []string{"cody@wrkq:primary", "lance"}}
	detail := strings.Join(wrkcRoomDetailLines(room, identity), "\n")
	if !strings.Contains(detail, "members: cody@wrkq:primary, lance") || strings.Contains(detail, "subject:") {
		t.Fatalf("ad-hoc detail did not render member identity:\n%s", detail)
	}
}
