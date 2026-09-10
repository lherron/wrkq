package style

import (
	"fmt"
	"os"
	"time"
)

// NowUTC returns the current UTC time, or the RFC3339 instant in WRKQ_NOW when
// set. The override exists ONLY to make relative-age rendering deterministic for
// byte-parity tests of --pretty output; production leaves WRKQ_NOW unset.
func NowUTC() time.Time {
	if v := os.Getenv("WRKQ_NOW"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

// DisplayLocation is the zone a human-facing render shows wall-clock times in.
// Stored timestamps are UTC and machine output never moves off it — that is the
// wire contract — but a log is read by a person sitting in one place, so the
// human porcelain shows THEIR clock.
//
// TZ decides, exactly as it does for every other unix tool, and unset means the
// host zone. It is read here rather than left to time.Local because time.Local
// is resolved once and cached, which would make the zone untestable and would
// pin a long-lived `--follow` to the zone it started in. Resolving per render is
// the same call T-08225 made for terminal width.
func DisplayLocation() *time.Location {
	if tz := os.Getenv("TZ"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.Local
}

// ParseTimestamp parses the timestamp formats wrkq stores, normalized to UTC.
func ParseTimestamp(timestamp string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		if parsed, err := time.Parse(layout, timestamp); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

// FormatDuration renders an elapsed duration as a single coarse unit, e.g.
// "3 days", "1 hour", or "less than a minute".
func FormatDuration(elapsed time.Duration) string {
	units := []struct {
		name    string
		seconds int64
	}{
		{"year", 365 * 24 * 60 * 60},
		{"month", 30 * 24 * 60 * 60},
		{"week", 7 * 24 * 60 * 60},
		{"day", 24 * 60 * 60},
		{"hour", 60 * 60},
		{"minute", 60},
	}

	elapsedSeconds := int64(elapsed.Seconds())
	for _, unit := range units {
		if elapsedSeconds >= unit.seconds {
			value := elapsedSeconds / unit.seconds
			name := unit.name
			if value != 1 {
				name += "s"
			}
			return fmt.Sprintf("%d %s", value, name)
		}
	}
	return "less than a minute"
}

// FormatOpenedAge renders the coarse age of a timestamp relative to NowUTC,
// e.g. "3 days" — empty when the timestamp can't be parsed. Callers append
// their own "opened …/ago" framing.
func FormatOpenedAge(timestamp string) string {
	createdAt, ok := ParseTimestamp(timestamp)
	if !ok {
		return ""
	}
	elapsed := NowUTC().Sub(createdAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return FormatDuration(elapsed)
}

// ShortStamp trims an RFC3339-ish timestamp to its date for compact display.
func ShortStamp(ts string) string {
	if len(ts) >= 10 && ts[4] == '-' && ts[7] == '-' {
		return ts[:10]
	}
	return ts
}
