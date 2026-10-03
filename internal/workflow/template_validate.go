//go:build wrkq_local

package workflow

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func LoadTemplateFile(path string) (*Template, []byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", err
	}
	return ParseTemplateContent(data)
}

// ParseTemplateContent parses a caller-supplied template body. RPC callers use
// this content boundary so paths are never interpreted on the server host.
func ParseTemplateContent(data []byte) (*Template, []byte, string, error) {
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		return nil, nil, "", err
	}
	return tpl, canonical, Hash(canonical), nil
}

func ParseTemplate(data []byte) (*Template, []byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	var tpl Template
	if err := json.Unmarshal(data, &tpl); err != nil {
		return nil, nil, err
	}
	tpl.Raw = raw
	canonical, err := json.Marshal(tplForHash(tpl))
	if err != nil {
		return nil, nil, err
	}
	return &tpl, canonical, nil
}

func tplForHash(t Template) map[string]interface{} {
	b, _ := json.Marshal(t)
	var out map[string]interface{}
	_ = json.Unmarshal(b, &out)
	return out
}

func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func previousEventHashTx(tx *sql.Tx, instanceID string) string {
	var prev sql.NullString
	_ = tx.QueryRow(`SELECT event_hash FROM workflow_events WHERE instance_id = ? ORDER BY seq DESC LIMIT 1`, instanceID).Scan(&prev)
	if prev.Valid {
		return prev.String
	}
	return ""
}

func chainedEventHash(prev string, payloadJSON []byte) string {
	chainPayload, _ := json.Marshal(map[string]interface{}{
		"prevEventHash": prev,
		"payloadHash":   Hash(payloadJSON),
	})
	return Hash(chainPayload)
}

func canonicalHookCatalog(catalog *HookCatalog) ([]byte, string, error) {
	if catalog == nil {
		return nil, "", nil
	}
	canonical, err := json.Marshal(catalog)
	if err != nil {
		return nil, "", err
	}
	return canonical, Hash(canonical), nil
}

func (s *Service) ValidateTemplateFile(path string, catalog *HookCatalog) ValidateResult {
	tpl, canonical, hash, err := LoadTemplateFile(path)
	return validateTemplateResult(tpl, canonical, hash, err, catalog)
}

// ValidateTemplateContent validates a decoded template body without consulting
// the daemon filesystem.
func (s *Service) ValidateTemplateContent(data []byte, catalog *HookCatalog) ValidateResult {
	tpl, canonical, hash, err := ParseTemplateContent(data)
	return validateTemplateResult(tpl, canonical, hash, err, catalog)
}

func validateTemplateResult(tpl *Template, canonical []byte, hash string, err error, catalog *HookCatalog) ValidateResult {
	if err != nil {
		return ValidateResult{Valid: false, Errors: []string{err.Error()}}
	}
	result := ValidateResult{Valid: true, ID: tpl.ID, Version: tpl.Version, Hash: hash}
	errs := ValidateTemplate(tpl, canonical, catalog)
	if len(errs) > 0 {
		result.Valid = false
		result.Errors = errs
	}
	return result
}

func ValidateTemplate(tpl *Template, canonical []byte, catalog *HookCatalog) []string {
	var errs []string
	if tpl.SchemaVersion != "wrkf.workflow-template.v0" {
		errs = append(errs, "schemaVersion must be wrkf.workflow-template.v0")
	}
	if tpl.ID == "" {
		errs = append(errs, "id is required")
	}
	if tpl.Version == "" {
		errs = append(errs, "version is required")
	}
	if tpl.Kind != "agent_first_workflow" {
		errs = append(errs, "kind must be agent_first_workflow")
	}
	if len(tpl.Roles) == 0 {
		errs = append(errs, "roles must not be empty")
	}
	if len(tpl.States) == 0 {
		errs = append(errs, "states must not be empty")
	}
	if len(tpl.Transitions) == 0 {
		errs = append(errs, "transitions must not be empty")
	}
	if containsInlineExecutable(canonical) {
		errs = append(errs, "template must not contain inline executable command keys")
	}
	errs = append(errs, validateFactsContracts(tpl)...)

	stateSet := map[string]bool{}
	for _, st := range tpl.States {
		if !validStatus(st.Status) {
			errs = append(errs, fmt.Sprintf("invalid state status %q", st.Status))
		}
		key := stateKey(st)
		if stateSet[key] {
			errs = append(errs, fmt.Sprintf("duplicate state %s", key))
		}
		stateSet[key] = true
	}
	if !stateSet[stateKey(tpl.Initial)] {
		errs = append(errs, "initial state must be listed in states")
	}
	transitions := map[string]bool{}
	suspendOutcomeCount := 0
	suspendReasonUsage := map[string]int{}
	for _, tr := range tpl.Transitions {
		if tr.ID == "" {
			errs = append(errs, "transition id is required")
			continue
		}
		if transitions[tr.ID] {
			errs = append(errs, fmt.Sprintf("duplicate transition %s", tr.ID))
		}
		transitions[tr.ID] = true
		if len(tr.FromAny) > 0 {

			if stateKey(tr.From) != stateKey(State{}) && !stateSet[stateKey(tr.From)] {
				errs = append(errs, fmt.Sprintf("transition %s from state is not declared", tr.ID))
			}
			for i, st := range tr.FromAny {
				if !stateSet[stateKey(st)] {
					errs = append(errs, fmt.Sprintf("transition %s fromAny[%d] state is not declared", tr.ID, i))
				}
			}
		} else if !stateSet[stateKey(tr.From)] {
			errs = append(errs, fmt.Sprintf("transition %s from state is not declared", tr.ID))
		}
		if len(tr.By) == 0 {
			errs = append(errs, fmt.Sprintf("transition %s must declare by roles", tr.ID))
		}
		for _, role := range tr.By {
			if _, ok := tpl.Roles[role]; !ok && role != "supervisor" && role != "system" {
				errs = append(errs, fmt.Sprintf("transition %s references unknown role %s", tr.ID, role))
			}
		}
		if tr.Responsibility != nil && tr.Responsibility.Role != "" {
			role := tr.Responsibility.Role
			if _, ok := tpl.Roles[role]; !ok && role != "supervisor" && role != "system" {
				errs = append(errs, fmt.Sprintf("transition %s responsibility references unknown role %s", tr.ID, role))
			}
		}
		for _, req := range tr.Requires {
			if req.Evidence != nil && tpl.EvidenceKinds != nil {
				if _, ok := tpl.EvidenceKinds[req.Evidence.Kind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s requires unknown evidence kind %s", tr.ID, req.Evidence.Kind))
				}
			}
			if req.Obligation != nil && tpl.ObligationKinds != nil && req.Obligation.Kind != "" {
				if _, ok := tpl.ObligationKinds[req.Obligation.Kind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s requires unknown obligation kind %s", tr.ID, req.Obligation.Kind))
				}
			}
		}
		if tr.SeparationOfDuty != nil && tpl.EvidenceKinds != nil {
			for _, kind := range tr.SeparationOfDuty.DistinctPrincipalFromEvidence {
				if _, ok := tpl.EvidenceKinds[kind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s SoD references unknown evidence kind %s", tr.ID, kind))
				}
			}
			for _, pair := range tr.SeparationOfDuty.EvidencePrincipalPairsDistinct {
				if _, ok := tpl.EvidenceKinds[pair.LeftKind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s SoD references unknown evidence kind %s", tr.ID, pair.LeftKind))
				}
				if _, ok := tpl.EvidenceKinds[pair.RightKind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s SoD references unknown evidence kind %s", tr.ID, pair.RightKind))
				}
			}
		}
		for _, checkID := range tr.Checks {
			check, ok := tpl.Checks[checkID]
			if !ok {
				errs = append(errs, fmt.Sprintf("transition %s references unknown check %s", tr.ID, checkID))
				continue
			}
			if check.Type == "hook" {
				if check.HookID == "" {
					errs = append(errs, fmt.Sprintf("check %s missing hookId", checkID))
				} else if catalog != nil {
					if _, ok := catalog.Hooks[check.HookID]; !ok {
						errs = append(errs, fmt.Sprintf("check %s references missing hook %s", checkID, check.HookID))
					}
				}
			}
		}
		if len(tr.Outcomes) == 0 {
			errs = append(errs, fmt.Sprintf("transition %s must declare outcomes", tr.ID))
		}
		for i, out := range tr.Outcomes {
			if out.ID == "" {
				errs = append(errs, fmt.Sprintf("transition %s outcome id is required", tr.ID))
			}
			hasTo := out.To != nil
			hasSuspend := out.Suspend != nil
			if hasTo == hasSuspend {
				errs = append(errs, fmt.Sprintf("transition %s outcome %s must declare exactly one of to or suspend", tr.ID, out.ID))
			}
			if hasTo && !stateSet[stateKey(*out.To)] {
				errs = append(errs, fmt.Sprintf("transition %s outcome %s target is not declared", tr.ID, out.ID))
			}
			if hasSuspend {
				suspendOutcomeCount++
				suspendReasonUsage[strings.TrimSpace(out.Suspend.Reason)]++
				if tr.From.Status == "closed" {
					errs = append(errs, fmt.Sprintf("transition %s outcome %s cannot suspend from a closed state", tr.ID, out.ID))
				}
				if tpl.Suspension == nil || !containsTrimmedString(tpl.Suspension.Reasons, strings.TrimSpace(out.Suspend.Reason)) {
					errs = append(errs, fmt.Sprintf("transition %s outcome %s suspend reason %q is not declared in suspension.reasons", tr.ID, out.ID, out.Suspend.Reason))
				}
			}
			if out.When.Otherwise != nil && *out.When.Otherwise && i != len(tr.Outcomes)-1 {
				errs = append(errs, fmt.Sprintf("transition %s otherwise outcome must be final", tr.ID))
			}
			for _, obl := range out.Obligations {
				if tpl.ObligationKinds != nil {
					if _, ok := tpl.ObligationKinds[obl.Kind]; !ok {
						errs = append(errs, fmt.Sprintf("transition %s outcome %s creates unknown obligation kind %s", tr.ID, out.ID, obl.Kind))
					}
				}
			}
		}
	}
	errs = append(errs, validateSuspensionPolicy(tpl.Suspension, suspendOutcomeCount, suspendReasonUsage)...)
	errs = append(errs, validateExecutableActions(tpl, stateSet, transitions)...)
	return errs
}

func validateSuspensionPolicy(spec *SuspensionPolicySpec, suspendOutcomeCount int, reasonUsage map[string]int) []string {
	if spec == nil {
		return nil
	}
	var errs []string
	if suspendOutcomeCount == 0 {
		errs = append(errs, "suspension is declared but no outcome uses suspend")
	}
	seen := map[string]bool{}
	for i, raw := range spec.Reasons {
		reason := strings.TrimSpace(raw)
		if reason == "" {
			errs = append(errs, fmt.Sprintf("suspension.reasons[%d] must not be empty", i))
			continue
		}
		if seen[reason] {
			errs = append(errs, fmt.Sprintf("duplicate suspension reason %q", reason))
			continue
		}
		seen[reason] = true
		if reasonUsage[reason] == 0 {
			errs = append(errs, fmt.Sprintf("suspension reason %q is declared but not referenced by any outcome", reason))
		}
	}
	if suspendOutcomeCount > 0 && len(seen) == 0 {
		errs = append(errs, "suspension.reasons must not be empty when an outcome uses suspend")
	}
	for disposition, effects := range spec.Effects {
		switch disposition {
		case "resume", "close", "cancel":
		default:
			errs = append(errs, fmt.Sprintf("suspension.effects contains unknown disposition %q", disposition))
		}
		for i, effect := range effects {
			if strings.TrimSpace(effect.Kind) == "" {
				errs = append(errs, fmt.Sprintf("suspension.effects.%s[%d] kind is required", disposition, i))
			}
		}
	}
	return errs
}

func containsTrimmedString(values []string, want string) bool {
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func validateExecutableActions(tpl *Template, stateSet map[string]bool, transitions map[string]bool) []string {
	if len(tpl.ExecutableActions) == 0 {
		return nil
	}
	var errs []string
	for actionID, spec := range tpl.ExecutableActions {
		id := strings.TrimSpace(spec.ID)
		if id == "" {
			id = strings.TrimSpace(actionID)
		}
		if id == "" {
			errs = append(errs, "executable action id is required")
			continue
		}
		label := "executable action " + id
		if spec.ID != "" && spec.ID != actionID {
			errs = append(errs, fmt.Sprintf("%s id %q must match map key %q", label, spec.ID, actionID))
		}
		role := strings.TrimSpace(spec.Role)
		if role == "" {
			errs = append(errs, fmt.Sprintf("%s role is required", label))
		} else if _, ok := tpl.Roles[role]; !ok && role != "supervisor" && role != "system" {
			errs = append(errs, fmt.Sprintf("%s references unknown role %s", label, role))
		}
		transitionID := strings.TrimSpace(spec.Transition)
		if transitionID == "" {
			errs = append(errs, fmt.Sprintf("%s transition is required", label))
		} else if !transitions[transitionID] {
			errs = append(errs, fmt.Sprintf("%s references unknown transition %s", label, transitionID))
		} else if tr, err := findTransition(tpl, transitionID); err == nil {
			if role != "" && !roleAllowed(role, tr.By) {
				errs = append(errs, fmt.Sprintf("%s role %s is not allowed by transition %s", label, role, transitionID))
			}
			if spec.From != nil && stateKey(*spec.From) != stateKey(State{}) {
				if !stateSet[stateKey(*spec.From)] {
					errs = append(errs, fmt.Sprintf("%s from state is not declared", label))
				} else if !stateCompatible(*spec.From, tr.From) {
					errs = append(errs, fmt.Sprintf("%s from state does not match transition %s from state", label, transitionID))
				}
			}
		}
		evidenceKind := strings.TrimSpace(spec.ResultEvidenceKind)
		if evidenceKind == "" {
			errs = append(errs, fmt.Sprintf("%s resultEvidenceKind is required", label))
		} else if _, ok := tpl.EvidenceKinds[evidenceKind]; !ok {
			errs = append(errs, fmt.Sprintf("%s references unknown evidence kind %s", label, evidenceKind))
		}
		if spec.Continuation != nil {
			next := strings.TrimSpace(spec.Continuation.Next)
			if next == "" {
				errs = append(errs, fmt.Sprintf("%s continuation next is required", label))
			} else if _, ok := tpl.ExecutableActions[next]; !ok {
				errs = append(errs, fmt.Sprintf("%s continuation targets missing executable action %s", label, next))
			}
			switch strings.TrimSpace(spec.Continuation.AttentionScope) {
			case "", "instance", "workspace":
			default:
				errs = append(errs, fmt.Sprintf("%s continuation attentionScope must be instance or workspace", label))
			}
		}
		if requiresSourceBinding(id, spec) {
			if spec.SourceBinding == nil {
				errs = append(errs, fmt.Sprintf("%s sourceBinding is required", label))
			} else {
				errs = append(errs, validateSourceBinding(tpl, label, spec.SourceBinding)...)
			}
		} else if spec.SourceBinding != nil {
			errs = append(errs, validateSourceBinding(tpl, label, spec.SourceBinding)...)
		}
		errs = append(errs, validateSettleValidation(tpl, label, spec)...)
		errs = append(errs, validateContextFreshness(tpl, label, spec)...)
		switch strings.TrimSpace(spec.WorkspaceMode) {
		case "", "none", "read-only", "exclusive":
		default:
			errs = append(errs, fmt.Sprintf("%s workspaceMode must be none, read-only, or exclusive", label))
		}
	}
	return errs
}

func validateContextFreshness(tpl *Template, label string, spec ExecutableActionSpec) []string {
	freshness := spec.ContextFreshness
	if freshness == nil {
		return nil
	}
	var errs []string
	verdictAction := strings.TrimSpace(freshness.VerdictAction)
	verdictSpec, ok := tpl.ExecutableActions[verdictAction]
	if verdictAction == "" {
		errs = append(errs, fmt.Sprintf("%s contextFreshness verdictAction is required", label))
	} else if !ok {
		errs = append(errs, fmt.Sprintf("%s contextFreshness references missing verdict action %s", label, verdictAction))
	}
	if spec.SourceBinding == nil {
		errs = append(errs, fmt.Sprintf("%s contextFreshness requires sourceBinding", label))
	} else if verdictAction != "" && strings.TrimSpace(spec.SourceBinding.Action) != verdictAction {
		errs = append(errs, fmt.Sprintf("%s contextFreshness verdictAction must match sourceBinding action", label))
	}
	passedFact := strings.TrimSpace(freshness.PassedFact)
	contextFact := strings.TrimSpace(freshness.ContextFact)
	if passedFact == "" {
		errs = append(errs, fmt.Sprintf("%s contextFreshness passedFact is required", label))
	}
	if contextFact == "" {
		errs = append(errs, fmt.Sprintf("%s contextFreshness contextFact is required", label))
	}
	if len(compactStrings(freshness.PassedValues)) == 0 {
		errs = append(errs, fmt.Sprintf("%s contextFreshness passedValues is required", label))
	}
	if ok {
		verdictKind, kindOK := tpl.EvidenceKinds[verdictSpec.ResultEvidenceKind]
		if !kindOK || verdictKind.Facts == nil || verdictKind.Facts.Properties == nil {
			errs = append(errs, fmt.Sprintf("%s contextFreshness verdict action must declare evidence facts", label))
		} else {
			for _, fact := range []string{passedFact, contextFact} {
				if fact == "" {
					continue
				}
				if _, declared := verdictKind.Facts.Properties[fact]; !declared {
					errs = append(errs, fmt.Sprintf("%s contextFreshness fact %s is not declared on %s", label, fact, verdictSpec.ResultEvidenceKind))
				}
			}
		}
	}
	lineageKindName := strings.TrimSpace(freshness.LineageKind)
	lineageKind, lineageOK := tpl.EvidenceKinds[lineageKindName]
	if lineageKindName == "" {
		errs = append(errs, fmt.Sprintf("%s contextFreshness lineageKind is required", label))
	} else if !lineageOK || lineageKind.Facts == nil || lineageKind.Facts.Properties == nil {
		errs = append(errs, fmt.Sprintf("%s contextFreshness lineage kind %s must declare facts", label, lineageKindName))
	} else if contextFact != "" {
		if _, declared := lineageKind.Facts.Properties[contextFact]; !declared {
			errs = append(errs, fmt.Sprintf("%s contextFreshness context fact %s is not declared on %s", label, contextFact, lineageKindName))
		}
	}
	return errs
}

func validateSettleValidation(tpl *Template, label string, spec ExecutableActionSpec) []string {
	if spec.SettleValidation == nil {
		return nil
	}
	var errs []string
	resultKind, ok := tpl.EvidenceKinds[spec.ResultEvidenceKind]
	if !ok || resultKind.Facts == nil || resultKind.Facts.Properties == nil {
		return []string{fmt.Sprintf("%s settlement validation requires declared result evidence facts", label)}
	}
	declaresResultFact := func(fact string) bool {
		_, ok := resultKind.Facts.Properties[fact]
		return ok
	}
	var sourceKind *KindSpec
	if spec.SourceBinding != nil {
		sourceAction := strings.TrimSpace(spec.SourceBinding.Action)
		if sourceSpec, ok := tpl.ExecutableActions[sourceAction]; ok {
			if kind, ok := tpl.EvidenceKinds[sourceSpec.ResultEvidenceKind]; ok {
				sourceKind = &kind
			}
		}
	}
	for i, rule := range spec.SettleValidation.Rules {
		ruleLabel := fmt.Sprintf("%s settleValidation.rules[%d]", label, i)
		for fact := range rule.WhenFacts {
			if !declaresResultFact(fact) {
				errs = append(errs, fmt.Sprintf("%s whenFacts fact %s is not declared on %s", ruleLabel, fact, spec.ResultEvidenceKind))
			}
		}
		for _, fact := range append(append([]string{}, rule.RequiredFacts...), rule.IdentityFact, rule.LinkageFact) {
			fact = strings.TrimSpace(fact)
			if fact == "" {
				continue
			}
			if !declaresResultFact(fact) {
				errs = append(errs, fmt.Sprintf("%s fact %s is not declared on %s", ruleLabel, fact, spec.ResultEvidenceKind))
			}
		}
		if (rule.IdentityFact != "" || rule.LinkageFact != "" || len(rule.EchoFields) > 0) && spec.SourceBinding == nil {
			errs = append(errs, fmt.Sprintf("%s source-derived checks require sourceBinding", ruleLabel))
		}
		for j, echo := range rule.EchoFields {
			echoLabel := fmt.Sprintf("%s echoFields[%d]", ruleLabel, j)
			if !declaresResultFact(strings.TrimSpace(echo.Fact)) {
				errs = append(errs, fmt.Sprintf("%s fact %s is not declared on %s", echoLabel, echo.Fact, spec.ResultEvidenceKind))
			}
			if sourceKind == nil || sourceKind.Facts == nil || sourceKind.Facts.Properties == nil {
				errs = append(errs, fmt.Sprintf("%s source fact %s is not declared by the source action", echoLabel, echo.SourceFact))
			} else if _, ok := sourceKind.Facts.Properties[strings.TrimSpace(echo.SourceFact)]; !ok {
				errs = append(errs, fmt.Sprintf("%s source fact %s is not declared by the source action", echoLabel, echo.SourceFact))
			}
		}
		for j, constraint := range rule.ValueConstraints {
			constraintLabel := fmt.Sprintf("%s valueConstraints[%d]", ruleLabel, j)
			if !declaresResultFact(strings.TrimSpace(constraint.Fact)) {
				errs = append(errs, fmt.Sprintf("%s fact %s is not declared on %s", constraintLabel, constraint.Fact, spec.ResultEvidenceKind))
			}
			if constraint.Equals == "" && constraint.EqualsFact == "" {
				errs = append(errs, fmt.Sprintf("%s requires equals or equalsFact", constraintLabel))
			}
			if constraint.Equals != "" && constraint.EqualsFact != "" {
				errs = append(errs, fmt.Sprintf("%s must not set both equals and equalsFact", constraintLabel))
			}
			if constraint.EqualsFact != "" && !declaresResultFact(strings.TrimSpace(constraint.EqualsFact)) {
				errs = append(errs, fmt.Sprintf("%s equalsFact %s is not declared on %s", constraintLabel, constraint.EqualsFact, spec.ResultEvidenceKind))
			}
		}
	}
	return errs
}

func requiresSourceBinding(id string, spec ExecutableActionSpec) bool {
	switch strings.TrimSpace(id) {
	case "verify", "landing":
		return true
	default:
		return false
	}
}

func validateSourceBinding(tpl *Template, label string, binding *SourceBindingSpec) []string {
	var errs []string
	if binding == nil {
		return errs
	}
	if strings.TrimSpace(binding.Kind) != "previous_action" {
		errs = append(errs, fmt.Sprintf("%s sourceBinding kind must be previous_action", label))
	}
	sourceAction := strings.TrimSpace(binding.Action)
	if sourceAction == "" {
		errs = append(errs, fmt.Sprintf("%s sourceBinding action is required", label))
	} else if _, ok := tpl.ExecutableActions[sourceAction]; !ok {
		errs = append(errs, fmt.Sprintf("%s sourceBinding references missing executable action %s", label, sourceAction))
	}
	if len(binding.RequiredFacts) > 0 && sourceAction != "" {
		if sourceSpec, ok := tpl.ExecutableActions[sourceAction]; ok {
			kind, ok := tpl.EvidenceKinds[sourceSpec.ResultEvidenceKind]
			for _, fact := range binding.RequiredFacts {
				fact = strings.TrimSpace(fact)
				if fact == "" {
					errs = append(errs, fmt.Sprintf("%s sourceBinding requiredFacts must not contain empty entries", label))
					continue
				}
				if !ok || kind.Facts == nil || kind.Facts.Properties == nil {
					errs = append(errs, fmt.Sprintf("%s sourceBinding required fact %s is not declared on %s", label, fact, sourceSpec.ResultEvidenceKind))
					continue
				}
				if _, exists := kind.Facts.Properties[fact]; !exists {
					errs = append(errs, fmt.Sprintf("%s sourceBinding required fact %s is not declared on %s", label, fact, sourceSpec.ResultEvidenceKind))
				}
			}
		}
	}
	if binding.BindFields != nil && sourceAction != "" {
		if sourceSpec, ok := tpl.ExecutableActions[sourceAction]; ok {
			kind, ok := tpl.EvidenceKinds[sourceSpec.ResultEvidenceKind]
			for field, fact := range map[string]string{
				"sourceIdentity": binding.BindFields.SourceIdentity,
				"artifactRef":    binding.BindFields.ArtifactRef,
			} {
				fact = strings.TrimSpace(fact)
				if fact == "" {
					continue
				}
				if !ok || kind.Facts == nil || kind.Facts.Properties == nil {
					errs = append(errs, fmt.Sprintf("%s sourceBinding bindFields.%s fact %s is not declared on %s", label, field, fact, sourceSpec.ResultEvidenceKind))
					continue
				}
				if _, exists := kind.Facts.Properties[fact]; !exists {
					errs = append(errs, fmt.Sprintf("%s sourceBinding bindFields.%s fact %s is not declared on %s", label, field, fact, sourceSpec.ResultEvidenceKind))
				}
			}
		}
	}
	return errs
}

func stateCompatible(actionFrom, transitionFrom State) bool {
	if actionFrom.Status != "" && transitionFrom.Status != "" && actionFrom.Status != transitionFrom.Status {
		return false
	}
	if actionFrom.Phase != "" && transitionFrom.Phase != "" && actionFrom.Phase != transitionFrom.Phase {
		return false
	}
	if actionFrom.Outcome != "" && transitionFrom.Outcome != "" && actionFrom.Outcome != transitionFrom.Outcome {
		return false
	}
	return true
}

func containsInlineExecutable(canonical []byte) bool {
	var walk func(interface{}) bool
	var v interface{}
	if err := json.Unmarshal(canonical, &v); err != nil {
		return true
	}
	forbidden := map[string]bool{"argv": true, "cmd": true, "command": true, "shell": true, "cwd": true, "env": true}
	walk = func(x interface{}) bool {
		switch t := x.(type) {
		case map[string]interface{}:
			for k, v := range t {
				if forbidden[k] {
					return true
				}
				if walk(v) {
					return true
				}
			}
		case []interface{}:
			for _, v := range t {
				if walk(v) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

func validStatus(status string) bool {
	switch status {
	case "open", "active", "waiting", "closed":
		return true
	default:
		return false
	}
}

func stateKey(st State) string {
	return st.Status + "/" + st.Phase + "/" + st.Outcome
}

func stateMatches(inst Instance, st State) bool {
	if st.Status != "" && inst.Status != st.Status {
		return false
	}
	if st.Phase != "" && inst.Phase != st.Phase {
		return false
	}
	if st.Outcome != "" && inst.Outcome != st.Outcome {
		return false
	}
	return true
}

// stateMatchesGuarded is stateMatches with a closed-state safety guard: a
// wildcard/blank-status from-state never implicitly matches a CLOSED instance.
// Only a from-state that names status "closed" explicitly matches a closed
// instance, so a blank/wildcard operator transition cannot reopen closed work.
func stateMatchesGuarded(inst Instance, st State) bool {
	if inst.Status == "closed" && st.Status != "closed" {
		return false
	}
	return stateMatches(inst, st)
}

// transitionFromMatches reports whether the instance satisfies a transition's
// source-state constraint: the single From state OR any FromAny entry, each
// evaluated with the closed-state guard. When FromAny is present, a zero-value
// From is NOT a catch-all wildcard — the source set is defined by FromAny (plus
// any explicit non-blank From); otherwise a blank From is the wildcard.
func transitionFromMatches(inst Instance, tr TransitionSpec) bool {
	if len(tr.FromAny) == 0 || stateKey(tr.From) != stateKey(State{}) {
		if stateMatchesGuarded(inst, tr.From) {
			return true
		}
	}
	for i := range tr.FromAny {
		if stateMatchesGuarded(inst, tr.FromAny[i]) {
			return true
		}
	}
	return false
}

// transitionFromRequirement renders a human-readable description of a
// transition's source-state constraint (From plus any FromAny) for blocked
// candidate messages.
func transitionFromRequirement(tr TransitionSpec) string {
	keys := make([]string, 0, len(tr.FromAny)+1)
	if len(tr.FromAny) == 0 || stateKey(tr.From) != stateKey(State{}) {
		keys = append(keys, stateKey(tr.From))
	}
	for i := range tr.FromAny {
		keys = append(keys, stateKey(tr.FromAny[i]))
	}
	return strings.Join(keys, " | ")
}
