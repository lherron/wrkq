package useragent

import "testing"

func TestCallerResolution(t *testing.T) {
	t.Setenv("PRAESIDIUM_CALLER", "parent")
	t.Setenv("ASP_HANDLE", "seat@wrkq:primary")
	if got := ResolveCaller("component"); got != "component" {
		t.Fatal(got)
	}
	if got := ResolveCaller(""); got != "parent" {
		t.Fatal(got)
	}
	t.Setenv("PRAESIDIUM_CALLER", "")
	if got := ResolveCaller(""); got != "seat@wrkq:primary" {
		t.Fatal(got)
	}
	t.Setenv("PRAESIDIUM_CALLER", "bad value")
	if got := ResolveCaller(""); got != "unknown" {
		t.Fatal(got)
	}
}
