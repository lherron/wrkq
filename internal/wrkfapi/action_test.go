//go:build wrkq_local

package wrkfapi

import (
	"encoding/json"
	"testing"
)

// TestCanonicalDeliveryRef locks the advertised `string | object` contract and
// the stable-key canonicalization for objects (daedalus risk: deliveryRef).
func TestCanonicalDeliveryRef(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"null", "null", "", false},
		{"string", `"acp:msg-1"`, "acp:msg-1", false},
		{"object stable keys", `{"z":1,"a":2}`, `{"a":2,"z":1}`, false},
		{"array rejected", `["a","b"]`, "", true},
		{"number rejected", `42`, "", true},
		{"bool rejected", `true`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalDeliveryRef(json.RawMessage(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("canonicalDeliveryRef(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
