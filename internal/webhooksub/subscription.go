// Package webhooksub owns the ON-THE-WIRE shape of a webhook subscription entry
// stored in containers.webhook_urls.
//
// A stored entry is EITHER a bare URL string (receive every event family) or a
// structured {"url":...,"events":[...]} object that narrows delivery to one or
// more event classes. Both forms coexist inside the same JSON array. This
// package is the single decoder/encoder for that array so the dispatcher
// (internal/webhooks), the write path (internal/wrkqapi), and the CLI
// (internal/rpccli) all agree on the grammar instead of each re-deriving it.
//
// Event MATCHING deliberately does NOT live here — it needs the dispatcher's
// event-family predicates. This package owns the wire shape only.
package webhooksub

import (
	"encoding/json"
	"errors"
	"strings"
)

// Subscription is one webhook_urls entry. An EMPTY Events means "no explicit
// narrowing" and receives every event family, which is exactly what a bare URL
// string means — so the bare and structured forms share one representation.
type Subscription struct {
	URL    string   `json:"url"`
	Events []string `json:"events,omitempty"`
}

// MarshalJSON emits the bare STRING form when there is no explicit event
// narrowing, so a list that was written as plain URLs round-trips byte-for-byte
// through decode/encode and legacy readers keep seeing plain strings.
func (s Subscription) MarshalJSON() ([]byte, error) {
	if len(s.Events) == 0 {
		return json.Marshal(s.URL)
	}
	type wire Subscription // sheds MarshalJSON; avoids infinite recursion
	return json.Marshal(wire(s))
}

// UnmarshalJSON accepts both entry forms: a bare URL string, or an object with
// url + optional events.
func (s *Subscription) UnmarshalJSON(data []byte) error {
	var bare string
	if err := json.Unmarshal(data, &bare); err == nil {
		s.URL = bare
		s.Events = nil
		return nil
	}
	var obj struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return errors.New(`webhook subscription must be a URL string or a {"url":...,"events":[...]} object`)
	}
	s.URL = obj.URL
	s.Events = obj.Events
	return nil
}

// Decode parses a stored webhook_urls JSON array into subscriptions. A nil,
// empty, or malformed value yields nil (legacy tolerance: a bad column never
// fails a read).
func Decode(raw *string) []Subscription {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	subs, err := DecodeStrict(*raw)
	if err != nil {
		return nil
	}
	return subs
}

// DecodeStrict parses a webhook_urls JSON array and reports malformed input.
// The dispatcher uses this so a corrupt column surfaces as an error rather than
// silently dropping every subscription on the container.
func DecodeStrict(raw string) ([]Subscription, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, err
	}
	out := make([]Subscription, 0, len(entries))
	for _, entry := range entries {
		var sub Subscription
		if err := sub.UnmarshalJSON(entry); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, nil
}

// Encode marshals subscriptions back to the stored JSON array form. A nil slice
// encodes as [] (never null), matching what the write path has always stored.
func Encode(subs []Subscription) (string, error) {
	if subs == nil {
		subs = []Subscription{}
	}
	payload, err := json.Marshal(subs)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// URLs projects the URL of each subscription, for surfaces that only expose
// URLs (e.g. the global webhook list view).
func URLs(subs []Subscription) []string {
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.URL)
	}
	return out
}

// Equal reports whether two subscription lists are identical in order, URL, and
// event narrowing. Used by the write path to decide whether a delta changed
// anything.
func Equal(a, b []Subscription) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].URL != b[i].URL || len(a[i].Events) != len(b[i].Events) {
			return false
		}
		for j := range a[i].Events {
			if a[i].Events[j] != b[i].Events[j] {
				return false
			}
		}
	}
	return true
}
