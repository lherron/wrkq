package style

import "testing"

func TestFormatOpenedAge(t *testing.T) {
	// Pin "now" via WRKQ_NOW so the relative-age formatting is deterministic.
	t.Setenv("WRKQ_NOW", "2026-06-12T15:04:05Z")

	tests := []struct {
		name      string
		timestamp string
		want      string
	}{
		{"less than minute", "2026-06-12T15:03:30Z", "less than a minute"},
		{"minutes", "2026-06-12T14:59:05Z", "5 minutes"},
		{"singular hour", "2026-06-12T13:34:05Z", "1 hour"},
		{"sqlite datetime", "2026-06-12 12:04:05", "3 hours"},
		{"days", "2026-06-09T15:04:05Z", "3 days"},
		{"unparseable", "not-a-time", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatOpenedAge(tt.timestamp); got != tt.want {
				t.Fatalf("FormatOpenedAge(%q) = %q, want %q", tt.timestamp, got, tt.want)
			}
		})
	}
}

func TestNowUTCHonorsOverride(t *testing.T) {
	t.Setenv("WRKQ_NOW", "2026-06-12T15:04:05Z")
	if got := NowUTC().Format("2006-01-02T15:04:05Z"); got != "2026-06-12T15:04:05Z" {
		t.Fatalf("NowUTC() with override = %q", got)
	}
}
