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

func TestHumanTimestampsUseLocalTwelveHourClock(t *testing.T) {
	t.Setenv("TZ", "America/Chicago")

	for _, tc := range []struct {
		name      string
		timestamp string
		want      string
	}{
		{name: "midnight crosses to prior evening", timestamp: "2026-09-10T02:30:00Z", want: "2026-09-09 9:30 PM CDT"},
		{name: "noon is PM", timestamp: "2026-09-10T17:00:00Z", want: "2026-09-10 12:00 PM CDT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatLocalTimestamp(tc.timestamp); got != tc.want {
				t.Fatalf("FormatLocalTimestamp(%q) = %q, want %q", tc.timestamp, got, tc.want)
			}
		})
	}

	if got := FormatLocalTimestamp("not-a-timestamp"); got != "not-a-timestamp" {
		t.Fatalf("unparseable timestamp = %q, want original value", got)
	}
	if got := ShortStamp("2026-09-10T02:30:00Z"); got != "2026-09-09" {
		t.Fatalf("ShortStamp did not use the local calendar day: %q", got)
	}
}
