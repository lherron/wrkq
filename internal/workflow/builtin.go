package workflow

import (
	_ "embed"
	"fmt"
	"strings"
)

// BuiltinSimpleTaskTemplateRef is the canonical ref for the built-in
// low-ceremony task lifecycle workflow used by wrkf.action.* when a caller does
// not supply an explicit workflow.
const BuiltinSimpleTaskTemplateRef = "wrkq-simple-task@1"
const BuiltinSimpleTaskV2TemplateRef = "wrkq-simple-task@2"
const BuiltinSimpleTaskV3TemplateRef = "wrkq-simple-task@3"
const BuiltinSimpleTaskV5TemplateRef = "wrkq-simple-task@5"
const BuiltinRoom2BoxTemplateRef = "room-2box@1"

//go:embed builtins/wrkq-simple-task.workflow.json
var builtinSimpleTaskJSON []byte

//go:embed builtins/wrkq-simple-task-v2.workflow.json
var builtinSimpleTaskV2JSON []byte

//go:embed builtins/wrkq-simple-task-v3.workflow.json
var builtinSimpleTaskV3JSON []byte

//go:embed builtins/wrkq-simple-task-v5.workflow.json
var builtinSimpleTaskV5JSON []byte

//go:embed builtins/room-2box.workflow.json
var builtinRoom2BoxJSON []byte

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
	case BuiltinSimpleTaskV2TemplateRef:
		return builtinSimpleTaskV2JSON, nil
	case BuiltinSimpleTaskV3TemplateRef:
		return builtinSimpleTaskV3JSON, nil
	case BuiltinSimpleTaskV5TemplateRef:
		return builtinSimpleTaskV5JSON, nil
	case BuiltinRoom2BoxTemplateRef:
		return builtinRoom2BoxJSON, nil
	default:
		return nil, fmt.Errorf("unknown built-in workflow: %s", templateRef)
	}
}

func (s *Service) EnsureInstanceBuiltinTemplate(inst *Instance, actor string) error {
	if inst == nil {
		return nil
	}
	ref := inst.TemplateID + "@" + inst.TemplateVersion
	if _, err := builtinTemplateData(ref); err != nil {
		return nil
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "wrkf:builtin"
	}
	_, _, err := s.EnsureBuiltinTemplate(ref, actor)
	return err
}

func (s *Service) EnsureBuiltinTemplateForSelectors(taskSelector, instanceID, actor string) error {
	inst, err := resolveInstanceSelectors(s.db, taskSelector, instanceID)
	if err != nil {
		return err
	}
	return s.EnsureInstanceBuiltinTemplate(inst, actor)
}
