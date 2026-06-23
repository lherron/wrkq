package rpccli

import "testing"

// TestCommentDeleteParams_ForwardsIfMatch proves the mirror forwards the etag
// precondition to wrkq.comment.delete (the server CAS) when --if-match was
// supplied — closing the show→delete race — and OMITS it otherwise. This is the
// param-construction guard for the MEDIUM fix in hrcchat#10198; the warn/skip
// MISMATCH branch is proven end-to-end by TestParity/comment-rm/if-match-*.
func TestCommentDeleteParams_ForwardsIfMatch(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		actor       string
		ifMatch     int64
		wantIfMatch bool
		wantActor   bool
	}{
		{name: "soft-with-ifmatch", mode: "soft", ifMatch: 7, wantIfMatch: true},
		{name: "purge-with-ifmatch", mode: "purge", ifMatch: 3, wantIfMatch: true},
		{name: "no-ifmatch-omitted", mode: "soft", ifMatch: 0, wantIfMatch: false},
		{name: "negative-ifmatch-omitted", mode: "soft", ifMatch: -1, wantIfMatch: false},
		{name: "actor-forwarded", mode: "soft", actor: "agent:x", ifMatch: 2, wantIfMatch: true, wantActor: true},
		{name: "empty-actor-omitted", mode: "soft", actor: "", ifMatch: 0, wantActor: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := commentDeleteParams("cu-123", tc.mode, tc.actor, tc.ifMatch)

			if got := p["id"]; got != "cu-123" {
				t.Errorf("id: want cu-123, got %v", got)
			}
			if got := p["mode"]; got != tc.mode {
				t.Errorf("mode: want %q, got %v (mode must ALWAYS be explicit)", tc.mode, got)
			}

			v, present := p["ifMatch"]
			if present != tc.wantIfMatch {
				t.Errorf("ifMatch presence: want %v, got %v (params=%v)", tc.wantIfMatch, present, p)
			}
			if tc.wantIfMatch {
				if got, ok := v.(int64); !ok || got != tc.ifMatch {
					t.Errorf("ifMatch value: want int64(%d), got %v (%T)", tc.ifMatch, v, v)
				}
			}

			if _, present := p["actor"]; present != tc.wantActor {
				t.Errorf("actor presence: want %v, got %v", tc.wantActor, present)
			}
		})
	}
}
