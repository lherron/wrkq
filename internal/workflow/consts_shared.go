package workflow

// Shared constants and options that are part of the wire-reachable surface
// (T-07090). These stay untagged so the portable client can compile the DTO
// graph without linking the store-backed workflow service.

const (
	// TransitionDefault resolves the unique transition available from the
	// current state for the run's role.
	TransitionDefault TransitionMode = iota
	// TransitionSkip records evidence and finishes the run without a transition.
	TransitionSkip
	// TransitionExplicit applies the caller-supplied transition id.
	TransitionExplicit
)

// BuiltinSimpleTaskTemplateRef is the canonical ref for the historical v1
// built-in. It remains addressable for explicit selection and existing
// instances.
const BuiltinSimpleTaskTemplateRef = "wrkq-simple-task@1"
const BuiltinSimpleTaskV2TemplateRef = "wrkq-simple-task@2"
const BuiltinSimpleTaskV3TemplateRef = "wrkq-simple-task@3"
const BuiltinSimpleTaskV5TemplateRef = "wrkq-simple-task@5"
const BuiltinRoom2BoxTemplateRef = "room-2box@1"

// DefaultActionWorkflowTemplateRef is the producer-owned workflow selected by
// wrkf.action.start when the caller does not supply an explicit workflow.
const DefaultActionWorkflowTemplateRef = BuiltinSimpleTaskV5TemplateRef

// AttachTaskOptions controls wrkf instance attachment.
type AttachTaskOptions struct {
	Supersede             bool
	PredecessorInstanceID string
	PredecessorRevision   *int64
	AttachDiscontinued    bool
}
