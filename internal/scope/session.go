package scope

import (
	"fmt"
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

// SessionRef is a scope ref plus the lane of that scope a runtime runs in.
type SessionRef struct {
	ScopeRef string
	LaneRef  string
}

// ParseSessionHandle parses "<scopeHandle>[~<laneId>]". The lane defaults to
// MainLane and is validated when present.
func ParseSessionHandle(handle string) (SessionRef, error) {
	scopePart, laneID, hasLane := strings.Cut(handle, "~")
	parsed, err := ParseScopeHandle(scopePart)
	if err != nil {
		return SessionRef{}, err
	}
	lane := MainLane
	if hasLane {
		ref := laneRefFromID(laneID)
		if lane, err = NormalizeLaneRef(&ref); err != nil {
			return SessionRef{}, err
		}
	}
	return SessionRef{ScopeRef: parsed.ScopeRef, LaneRef: lane}, nil
}

// ParseSessionRef parses a canonical session ref, "<scopeRef>/lane:<laneId>",
// as HRC writes HRC_SESSION_REF. Surrounding whitespace is trimmed from the
// input, the scope part and the lane id. The scope part must be a canonical
// scope ref: "cody@wrkq/lane:main" is refused. "lane:main" reads as MainLane.
func ParseSessionRef(input string) (SessionRef, error) {
	normalized := strings.TrimSpace(input)
	parts := strings.Split(normalized, "/")
	if len(parts) != 2 || !strings.HasPrefix(parts[1], lanePrefix) {
		code := CodeShapeInvalid
		if normalized == "" {
			code = CodeEmpty
		}
		return SessionRef{}, &GrammarError{Code: code, Message: fmt.Sprintf(`invalid SessionRef %q: expected "<scopeRef>/lane:<laneId>"`, input)}
	}
	scopeRef := strings.TrimSpace(parts[0])
	laneID := strings.TrimSpace(strings.TrimPrefix(parts[1], lanePrefix))
	if scopeRef == "" || laneID == "" {
		code := CodeTokenInvalid
		if scopeRef == "" {
			code = CodeShapeInvalid
		}
		return SessionRef{}, &GrammarError{Code: code, Message: fmt.Sprintf("invalid SessionRef %q: scopeRef and laneRef are required", input)}
	}
	if v := ValidateScopeRef(scopeRef); !v.OK {
		return SessionRef{}, &GrammarError{Code: v.Code, Message: fmt.Sprintf("invalid SessionRef %q: invalid ScopeRef %q: %s", input, scopeRef, v.Error)}
	}
	ref := laneRefFromID(laneID)
	lane, err := NormalizeLaneRef(&ref)
	if err != nil {
		return SessionRef{}, err
	}
	return SessionRef{ScopeRef: scopeRef, LaneRef: lane}, nil
}

// SeatScope returns the scope a caller-supplied value names. A value naming a
// lane ("/lane:", as HRC's session refs do) is parsed by ParseSessionRef and
// its canonical scope ref returned; a refusal is returned as is. Any other
// value is returned unchanged for the caller's own scope parse.
func SeatScope(raw string) (string, error) {
	if !strings.Contains(raw, "/"+lanePrefix) {
		return raw, nil
	}
	session, err := ParseSessionRef(raw)
	if err != nil {
		return "", err
	}
	return session.ScopeRef, nil
}

// SessionRefFromEnv returns a session only when HRC names the exact writer seat.
// HRC_SESSION_REF must be a canonical session ref whose scope is scopeRef.
func SessionRefFromEnv(scopeRef string) *HostSession {
	if scopeRef == "" {
		return nil
	}
	seat, err := ParseSessionRef(os.Getenv("HRC_SESSION_REF"))
	if err != nil || seat.ScopeRef != scopeRef {
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
