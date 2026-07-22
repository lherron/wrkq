package domain

import (
	"fmt"
	"strings"
)

// CommentKindVocabulary is the complete seeded vocabulary for typed judgment
// comments. Extending it requires architecture consultation and a matching
// S2-style schema migration so domain and database enforcement stay aligned.
const CommentKindVocabulary = "blocker|decision|postmortem|digest"

// ValidateCommentKind accepts nil for plain conversation and rejects every
// non-nil value outside CommentKindVocabulary.
func ValidateCommentKind(kind *string) error {
	if kind == nil {
		return nil
	}
	for _, allowed := range strings.Split(CommentKindVocabulary, "|") {
		if *kind == allowed {
			return nil
		}
	}
	return fmt.Errorf(
		"invalid comment kind %q: expected one of %s (or omit kind for plain conversation)",
		*kind,
		strings.ReplaceAll(CommentKindVocabulary, "|", ", "),
	)
}
