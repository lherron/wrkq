package paths

import "testing"

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		// Simple patterns
		{
			name:    "exact match",
			pattern: "hello",
			path:    "hello",
			want:    true,
		},
		{
			name:    "no match",
			pattern: "hello",
			path:    "world",
			want:    false,
		},

		// Star patterns
		{
			name:    "star prefix",
			pattern: "*.txt",
			path:    "file.txt",
			want:    true,
		},
		{
			name:    "star suffix",
			pattern: "file.*",
			path:    "file.txt",
			want:    true,
		},
		{
			name:    "star middle",
			pattern: "file*.txt",
			path:    "file123.txt",
			want:    true,
		},
		{
			name:    "star no match",
			pattern: "*.txt",
			path:    "file.md",
			want:    false,
		},

		// Question mark patterns
		{
			name:    "question mark",
			pattern: "file?.txt",
			path:    "file1.txt",
			want:    true,
		},
		{
			name:    "question mark no match",
			pattern: "file?.txt",
			path:    "file12.txt",
			want:    false,
		},

		// Path patterns
		{
			name:    "simple path match",
			pattern: "a/b/c",
			path:    "a/b/c",
			want:    true,
		},
		{
			name:    "simple path no match",
			pattern: "a/b/c",
			path:    "a/b/d",
			want:    false,
		},

		// Double star patterns
		{
			name:    "double star matches everything",
			pattern: "**/file.txt",
			path:    "a/b/c/file.txt",
			want:    true,
		},
		{
			name:    "double star at end",
			pattern: "a/**",
			path:    "a/b/c/d",
			want:    true,
		},
		{
			name:    "double star in middle",
			pattern: "a/**/d",
			path:    "a/b/c/d",
			want:    true,
		},
		{
			name:    "double star matches zero segments",
			pattern: "a/**/c",
			path:    "a/c",
			want:    true,
		},
		{
			name:    "double star matches one segment",
			pattern: "a/**/c",
			path:    "a/b/c",
			want:    true,
		},
		{
			name:    "double star matches multiple segments",
			pattern: "a/**/z",
			path:    "a/b/c/d/e/f/z",
			want:    true,
		},
		{
			name:    "double star no match",
			pattern: "a/**/c",
			path:    "a/b/d",
			want:    false,
		},
		{
			name:    "multiple double stars",
			pattern: "a/**/c/**/e",
			path:    "a/b/c/d/e",
			want:    true,
		},
		{
			name:    "double star with wildcards",
			pattern: "a/**/*.txt",
			path:    "a/b/c/file.txt",
			want:    true,
		},

		// Edge cases
		{
			name:    "empty pattern and path",
			pattern: "",
			path:    "",
			want:    true,
		},
		{
			name:    "empty path with pattern",
			pattern: "a",
			path:    "",
			want:    false,
		},
		{
			name:    "just double star matches everything",
			pattern: "**",
			path:    "a/b/c",
			want:    true,
		},
		{
			name:    "just double star matches empty",
			pattern: "**",
			path:    "",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchGlob(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("MatchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestIsGlobPattern(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "has star",
			input: "*.txt",
			want:  true,
		},
		{
			name:  "has question",
			input: "file?.txt",
			want:  true,
		},
		{
			name:  "has bracket",
			input: "file[1-3].txt",
			want:  true,
		},
		{
			name:  "has double star",
			input: "**/file.txt",
			want:  true,
		},
		{
			name:  "no glob chars",
			input: "simple-path",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsGlobPattern(tt.input)
			if got != tt.want {
				t.Errorf("IsGlobPattern(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// Benchmark glob matching
func BenchmarkMatchGlob(b *testing.B) {
	benchmarks := []struct {
		name    string
		pattern string
		path    string
	}{
		{
			name:    "simple match",
			pattern: "*.txt",
			path:    "file.txt",
		},
		{
			name:    "double star deep path",
			pattern: "**/file.txt",
			path:    "a/b/c/d/e/f/g/file.txt",
		},
		{
			name:    "complex pattern",
			pattern: "a/**/c/**/*.txt",
			path:    "a/b/c/d/e/file.txt",
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				MatchGlob(bm.pattern, bm.path)
			}
		})
	}
}
