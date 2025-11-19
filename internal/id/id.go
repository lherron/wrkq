package id

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	actorIDPattern     = regexp.MustCompile(`^A-\d{5}$`)
	containerIDPattern = regexp.MustCompile(`^P-\d{5}$`)
	taskIDPattern      = regexp.MustCompile(`^T-\d{5}$`)
	commentIDPattern   = regexp.MustCompile(`^C-\d{5}$`)
	attachmentIDPattern = regexp.MustCompile(`^ATT-\d{5}$`)
	uuidPattern        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// Type represents the type of resource
type Type string

const (
	TypeActor      Type = "actor"
	TypeContainer  Type = "container"
	TypeTask       Type = "task"
	TypeComment    Type = "comment"
	TypeAttachment Type = "attachment"
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

// FormatAttachment formats an attachment friendly ID
func FormatAttachment(seq int) string {
	return fmt.Sprintf("ATT-%05d", seq)
}

// Parse parses an ID string and returns the type and sequence number
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
	case attachmentIDPattern.MatchString(id):
		seq, _ := strconv.Atoi(id[4:])
		return TypeAttachment, seq, nil
	default:
		return "", 0, fmt.Errorf("invalid friendly ID format: %s", id)
	}
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
