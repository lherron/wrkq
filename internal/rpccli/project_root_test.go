package rpccli

import (
	"path/filepath"
	"testing"
)

func TestNormalizeRegisteredProjectRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inside := filepath.Join(home, "praesidium", "wrkq")
	outside := filepath.Join(filepath.Dir(home), "elsewhere", "wrkq")

	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "clear", input: "", want: ""},
		{name: "home", input: home, want: "~"},
		{name: "absolute under home", input: inside, want: "~/praesidium/wrkq"},
		{name: "tilde path", input: "~/praesidium/../praesidium/wrkq", want: "~/praesidium/wrkq"},
		{name: "absolute outside home", input: outside, want: filepath.Clean(outside)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeRegisteredProjectRoot(tc.input)
			if err != nil {
				t.Fatalf("normalizeRegisteredProjectRoot(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("normalizeRegisteredProjectRoot(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeRegisteredProjectRootRejectsOtherUserTilde(t *testing.T) {
	if _, err := normalizeRegisteredProjectRoot("~someone/repo"); err == nil {
		t.Fatal("expected ~user path to be rejected")
	}
}
