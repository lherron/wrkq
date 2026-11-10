package scope

import "errors"

// ErrorCode is the coarse refusal code the shared conformance fixture checks
// (agent-spaces contracts/agent-scope/conformance/README.md). Messages stay
// wrkq's own; callers that branch on a refusal branch on the code.
type ErrorCode string

const (
	// CodeEmpty: the operation's input is empty.
	CodeEmpty ErrorCode = "empty"
	// CodeTokenInvalid: a segment is not 1..64 characters of [A-Za-z0-9._-].
	CodeTokenInvalid ErrorCode = "token_invalid"
	// CodeShapeInvalid: segments, separators or their order are wrong.
	CodeShapeInvalid ErrorCode = "shape_invalid"
	// CodeDirectiveNotAllowed: a "+" directive where the operation takes none.
	CodeDirectiveNotAllowed ErrorCode = "directive_not_allowed"
	// CodeTaskRequiresProject: input resolution got a task and no project.
	CodeTaskRequiresProject ErrorCode = "task_requires_project"
)

// GrammarError is the error every grammar operation returns.
type GrammarError struct {
	Code    ErrorCode
	Message string
}

func (e *GrammarError) Error() string { return e.Message }

// CodeOf returns the refusal code carried by err or anything it wraps, or ""
// when err is not a grammar refusal.
func CodeOf(err error) ErrorCode {
	var ge *GrammarError
	if errors.As(err, &ge) {
		return ge.Code
	}
	return ""
}
