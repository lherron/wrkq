package id

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	actorIDPattern      = regexp.MustCompile(`^A-\d{5}$`)
	containerIDPattern  = regexp.MustCompile(`^P-\d{5}$`)
	taskIDPattern       = regexp.MustCompile(`^` + OrdinaryTaskPattern + `$`)
	commentIDPattern    = regexp.MustCompile(`^C-\d{5}$`)
	handoffIDPattern    = regexp.MustCompile(`^H-\d{5}$`)
	attachmentIDPattern = regexp.MustCompile(`^ATT-\d{5}$`)
	promiseIDPattern    = regexp.MustCompile(`^PR-\d{5}$`)
	roomIDPattern       = regexp.MustCompile(`^R-\d{5}$`)
	envelopeIDPattern   = regexp.MustCompile(`^EN-\d{5}$`)
	uuidPattern         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	bareSeqPattern      = regexp.MustCompile(`^\d{1,5}$`)
)

// OrdinaryTaskPattern is the sequence-bearing task ID grammar. Architecture
// exception markers remain ordinary-only and retain their own digit-width rules.
const OrdinaryTaskPattern = `T-\d{5}`

// TaskPattern is the shared task grammar, including named subtasks. Length is
// enforced by ParseTask rather than by truncating a prose match.
const TaskPattern = OrdinaryTaskPattern + `(?:\.[a-z](?:[a-z0-9-]*[a-z0-9])?)?`

var (
	completeTaskPattern = regexp.MustCompile(`^` + TaskPattern + `$`)
	taskProsePattern    = regexp.MustCompile(`\b` + TaskPattern + `\b`)
)

// ParseTask returns the ordinary owner ID and local slug (empty for an ordinary
// task). It accepts only exact task IDs of at most 64 ASCII characters.
func ParseTask(taskID string) (owner, slug string, err error) {
	if len(taskID) > 64 || !completeTaskPattern.MatchString(taskID) {
		return "", "", fmt.Errorf("invalid task ID format: %s", taskID)
	}
	owner, slug, _ = strings.Cut(taskID, ".")
	return owner, slug, nil
}

// IsTask recognizes both ordinary task and named subtask IDs.
func IsTask(taskID string) bool {
	_, _, err := ParseTask(taskID)
	return err == nil
}

// IsOrdinaryTask recognizes only sequence-bearing task IDs.
func IsOrdinaryTask(taskID string) bool {
	return taskIDPattern.MatchString(taskID)
}

// FindTaskIDs extracts task mentions in order, retaining duplicates. A sentence
// ending dot or uppercase suffix refers to the owner. Overlength composite
// matches are discarded whole, never truncated to their owner or a shorter slug.
func FindTaskIDs(prose string) []string {
	result := []string{}
	for _, taskID := range taskProsePattern.FindAllString(prose, -1) {
		if IsTask(taskID) {
			result = append(result, taskID)
		}
	}
	return result
}

// Type represents the type of resource
type Type string

const (
	TypeActor      Type = "actor"
	TypeContainer  Type = "container"
	TypeTask       Type = "task"
	TypeComment    Type = "comment"
	TypeHandoff    Type = "handoff"
	TypeAttachment Type = "attachment"
	TypePromise    Type = "promise"
	TypeRoom       Type = "room"
	TypeEnvelope   Type = "envelope"
)

// FormatActor formats an actor friendly ID
func FormatActor(seq int) string {
	return fmt.Sprintf("A-%05d", seq)
}

// FormatContainer formats a container friendly ID
func FormatContainer(seq int) string {
	return fmt.Sprintf("P-%05d", seq)
}

// FormatTask formats a task friendly ID
func FormatTask(seq int) string {
	return fmt.Sprintf("T-%05d", seq)
}

// FormatComment formats a comment friendly ID
func FormatComment(seq int) string {
	return fmt.Sprintf("C-%05d", seq)
}

// FormatHandoff formats a handoff friendly ID
func FormatHandoff(seq int) string {
	return fmt.Sprintf("H-%05d", seq)
}

// FormatAttachment formats an attachment friendly ID
func FormatAttachment(seq int) string {
	return fmt.Sprintf("ATT-%05d", seq)
}

// FormatPromise formats a promise friendly ID.
func FormatPromise(seq int) string {
	return fmt.Sprintf("PR-%05d", seq)
}

// FormatRoom formats an ad-hoc room friendly ID. Derived rooms (campaign,
// task, project) carry no friendly ID: their key is the work identity itself.
func FormatRoom(seq int) string {
	return fmt.Sprintf("R-%05d", seq)
}

// FormatEnvelope formats an envelope friendly ID. The prefix is EN-, not EV-:
// EV- is already owned by evidence_items (migration 000013) and is addressable
// through wrkf.evidence.show, so an envelope minted as EV- would put two
// addressable row kinds behind one id string.
func FormatEnvelope(seq int) string {
	return fmt.Sprintf("EN-%05d", seq)
}

// Parse parses a sequence-bearing ID and returns its type and sequence number.
// Use ParseTask for task identities that may include a named subtask.
func Parse(id string) (Type, int, error) {
	id = strings.TrimSpace(id)

	switch {
	case actorIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[2:])
		return TypeActor, seq, nil
	case containerIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[2:])
		return TypeContainer, seq, nil
	case taskIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[2:])
		return TypeTask, seq, nil
	case commentIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[2:])
		return TypeComment, seq, nil
	case handoffIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[2:])
		return TypeHandoff, seq, nil
	case attachmentIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[4:])
		return TypeAttachment, seq, nil
	case promiseIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[3:])
		return TypePromise, seq, nil
	case envelopeIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[3:])
		return TypeEnvelope, seq, nil
	case roomIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[2:])
		return TypeRoom, seq, nil
	default:
		return "", 0, fmt.Errorf("invalid friendly ID format: %s", id)
	}
}

// ExpandTaskID expands a bare sequence number into a full task friendly ID
// as a convenience (e.g. "1454" -> "T-01454"). It returns (expanded, true)
// when s is a bare 1-5 digit number, and (s, false) otherwise so callers can
// leave non-matching tokens untouched.
func ExpandTaskID(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !bareSeqPattern.MatchString(s) {
		return s, false
	}
	seq, err := strconv.Atoi(s)
	if err != nil {
		return s, false
	}
	return FormatTask(seq), true
}

// TaskIDCandidates lists the task friendly IDs a bare sequence number may
// name, highest first. A 1-4 digit number names the same low four digits in
// every ten-thousand block ("639" -> T-90639 ... T-10639, T-00639) so the
// shorthand keeps reaching current work after the sequence rolls past 10000;
// resolvers pick the highest candidate that exists. A 5-digit number is a
// literal ID and has exactly one candidate. It returns (nil, false) when s is
// not a bare 1-5 digit number.
func TaskIDCandidates(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if !bareSeqPattern.MatchString(s) {
		return nil, false
	}
	seq, err := strconv.Atoi(s)
	if err != nil {
		return nil, false
	}
	if len(s) == 5 {
		return []string{FormatTask(seq)}, true
	}
	candidates := make([]string, 0, 10)
	for block := 9; block >= 0; block-- {
		candidates = append(candidates, FormatTask(block*10000+seq))
	}
	return candidates, true
}

// IsUUID checks if a string is a valid UUID
func IsUUID(s string) bool {
	return uuidPattern.MatchString(strings.ToLower(s))
}

// IsFriendlyID checks if a string is a valid friendly ID
func IsFriendlyID(s string) bool {
	_, _, err := Parse(s)
	return err == nil
}
