package scope

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// HostSession is the caller's optional HRC runtime attribution.
type HostSession struct {
	HostSessionID string `json:"hostSessionId"`
	Generation    int64  `json:"generation"`
}

var hostSessionIDPattern = regexp.MustCompile(`^hsid-[0-9a-f-]{36}$`)

func ValidHostSession(hostSessionID string, generation int64) bool {
	return hostSessionIDPattern.MatchString(hostSessionID) && generation >= 1
}

// SessionRefFromEnv returns a session only when HRC names the exact writer seat.
func SessionRefFromEnv(scopeRef string) *HostSession {
	if scopeRef == "" {
		return nil
	}
	raw, _, _ := strings.Cut(os.Getenv("HRC_SESSION_REF"), "/lane:")
	if raw == "" {
		return nil
	}
	resolved, _, err := Resolve(raw)
	if err != nil || resolved.FullRef() != scopeRef {
		return nil
	}
	generation, err := strconv.ParseInt(os.Getenv("HRC_GENERATION"), 10, 64)
	if err != nil {
		return nil
	}
	session := &HostSession{HostSessionID: os.Getenv("HRC_HOST_SESSION_ID"), Generation: generation}
	if !ValidHostSession(session.HostSessionID, session.Generation) {
		return nil
	}
	return session
}
