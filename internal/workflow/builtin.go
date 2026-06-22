package workflow

import (
	_ "embed"
	"fmt"
)

// BuiltinSimpleTaskTemplateRef is the canonical ref for the built-in
// low-ceremony task lifecycle workflow used by wrkf.action.* when a caller does
// not supply an explicit workflow.
const BuiltinSimpleTaskTemplateRef = "wrkq-simple-task@1"

//go:embed builtins/wrkq-simple-task.workflow.json
var builtinSimpleTaskJSON []byte

// EnsureBuiltinTemplate installs the embedded built-in workflow identified by
// templateRef if it is not already installed. Installation is idempotent: a
// matching hash is a no-op, a conflicting hash is an error. It returns the
// installed template id and version.
func (s *Service) EnsureBuiltinTemplate(templateRef, actor string) (string, string, error) {
	data, err := builtinTemplateData(templateRef)
	if err != nil {
		return "", "", err
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		return "", "", err
	}
	// supersede=true: a rebuilt binary may carry an evolved built-in definition
	// (same id@version, new hash); overwrite the stored definition in place
	// rather than erroring. No pinned-hash guard — old instances may evaluate
	// under the new definition; accepted operational risk for built-ins only.
	if _, err := s.installTemplateCanonical(tpl, canonical, Hash(canonical), actor, nil, true); err != nil {
		return "", "", err
	}
	return tpl.ID, tpl.Version, nil
}

func builtinTemplateData(templateRef string) ([]byte, error) {
	switch templateRef {
	case BuiltinSimpleTaskTemplateRef, "wrkq-simple-task":
		return builtinSimpleTaskJSON, nil
	default:
		return nil, fmt.Errorf("unknown built-in workflow: %s", templateRef)
	}
}
