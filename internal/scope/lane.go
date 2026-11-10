package scope

import (
	"fmt"
	"strings"
)

const (
	// MainLane is the lane ref of the default lane.
	MainLane = "main"
	// lanePrefix opens the "lane:<laneId>" ref form.
	lanePrefix = "lane:"
)

// ValidateLaneRef validates a lane ref: "main" or "lane:<laneId>".
func ValidateLaneRef(laneRef string) ValidationResult {
	if laneRef == MainLane {
		return ValidationResult{OK: true}
	}
	if !strings.HasPrefix(laneRef, lanePrefix) {
		code := CodeShapeInvalid
		if laneRef == "" {
			code = CodeEmpty
		}
		return invalid(code, `LaneRef must be "main" or "lane:<laneId>"`)
	}
	if r, ok := tokenResult(strings.TrimPrefix(laneRef, lanePrefix), "laneId"); !ok {
		return r
	}
	return ValidationResult{OK: true}
}

// NormalizeLaneRef returns the canonical lane ref. An absent lane (nil) is
// MainLane; "lane:main" stays "lane:main", and "lane:default" is a lane named
// default (there is no default → main rewrite in the grammar).
func NormalizeLaneRef(laneRef *string) (string, error) {
	if laneRef == nil || *laneRef == MainLane {
		return MainLane, nil
	}
	if v := ValidateLaneRef(*laneRef); !v.OK {
		return "", &GrammarError{Code: v.Code, Message: fmt.Sprintf("invalid LaneRef %q: %s", *laneRef, v.Error)}
	}
	return *laneRef, nil
}

// laneRefFromID builds a lane ref from a bare lane id: "main" is the main lane.
func laneRefFromID(laneID string) string {
	if laneID == MainLane {
		return MainLane
	}
	return lanePrefix + laneID
}
