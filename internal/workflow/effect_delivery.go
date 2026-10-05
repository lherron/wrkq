//go:build wrkq_local

package workflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

const transitionBuiltinEffectAdapter = "wrkf-transition-builtin"

var engineOwnedBuiltinEffectKinds = map[string]struct{}{
	"set_task_state": {},
}

func isEngineOwnedBuiltinEffect(kind string) bool {
	_, ok := engineOwnedBuiltinEffectKinds[kind]
	return ok
}

func (s *Service) DeliverEffect(id, adapter string, catalog *HookCatalog, templateDir string) (*EffectDelivery, error) {
	return s.DeliverEffectWithOptions(id, adapter, catalog, templateDir, HookExecutionOptions{})
}

func (s *Service) DeliverEffectWithOptions(id, adapter string, catalog *HookCatalog, templateDir string, execOpts HookExecutionOptions) (*EffectDelivery, error) {
	current, err := s.ShowEffect(id)
	if err != nil {
		return nil, err
	}
	if current.Status == "delivered" {
		return &EffectDelivery{Effect: current}, nil
	}
	if current.Kind == "set_task_state" {
		return s.deliverSetTaskStateEffect(current, adapter)
	}
	instForCatalog, err := s.instanceByID(current.InstanceID)
	if err != nil {
		return nil, err
	}
	pinned, err := s.pinnedHookCatalog(instForCatalog.TemplateID, instForCatalog.TemplateVersion, catalog)
	if err != nil {
		return nil, err
	}
	handler, ok := pinned.EffectHandlers[current.Kind]
	if !ok {
		if h, hookOK := pinned.Hooks["effect_"+current.Kind]; hookOK {
			handler, ok = h, true
		}
	}
	if !ok {
		return nil, fmt.Errorf("no effect handler registered for %s", current.Kind)
	}

	if _, err := effectiveHookTimeout(handler, execOpts.TimeoutCeiling); err != nil {
		return nil, err
	}

	claim, err := s.claimEffectByID(id, adapter, 60_000)
	if err != nil {
		return nil, err
	}
	if claim == nil || len(claim.Effects) == 0 {
		return nil, leaseConflictError(id, "")
	}
	eff := &claim.Effects[0]
	if eff.Status == "delivered" {
		return &EffectDelivery{Effect: eff}, nil
	}
	inst, err := s.instanceByID(eff.InstanceID)
	if err != nil {
		return nil, err
	}
	task, _ := loadTaskDoc(s.db, inst.TaskUUID)
	role := effectRole(eff)
	if role == "" {
		return nil, fmt.Errorf("effect %s has no role binding target", eff.ID)
	}
	binding, err := s.latestRunForRole(inst.ID, role)
	if err != nil {
		return nil, err
	}
	ev, _ := listInstanceEvidence(s.db, inst.ID)
	input := map[string]interface{}{
		"effect":   eff,
		"instance": inst,
		"binding":  binding,
		"evidence": ev,
	}
	if task != nil {
		input["task"] = map[string]interface{}{"id": task.ID, "uuid": task.UUID, "title": task.Title, "state": task.State, "taskRef": "wrkq:" + task.ID}
	}
	inputJSON, _ := json.Marshal(input)
	execCtx := execOpts.context()
	exit, stdout, stderr, runErr := runHook(execCtx, handler, templateDir, inputJSON, execOpts.TimeoutCeiling)
	if ctxErr := execCtx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if runErr != nil && exit < 0 {
		return nil, runErr
	}
	out := &EffectDelivery{Effect: eff, Binding: binding, ExitCode: exit, Stdout: string(stdout), Stderr: string(stderr)}
	if json.Valid(stdout) {
		out.Receipt = json.RawMessage(stdout)
	}
	if exit != 0 {
		failed, failErr := s.FailEffect(id, claim.LeaseToken, strings.TrimSpace(string(stderr)), true)
		if failErr == nil {
			out.Effect = failed
		}
		return out, fmt.Errorf("effect handler failed with exit %d: %s", exit, strings.TrimSpace(string(stderr)))
	}
	delivered, err := s.AckEffectWithReceipt(id, claim.LeaseToken, out.Receipt)
	if err != nil {
		return nil, err
	}
	out.Effect = delivered
	return out, nil
}

func (s *Service) deliverSetTaskStateEffect(current *Effect, adapter string) (*EffectDelivery, error) {
	claim, err := s.claimEffectByID(current.ID, adapter, 60_000)
	if err != nil {
		return nil, err
	}
	if claim == nil || len(claim.Effects) == 0 {
		return nil, leaseConflictError(current.ID, "")
	}
	eff := &claim.Effects[0]
	if eff.Status == "delivered" {
		return &EffectDelivery{Effect: eff}, nil
	}
	var spec EffectSpec
	if err := json.Unmarshal(eff.Payload, &spec); err != nil {
		failed, failErr := s.FailEffect(eff.ID, claim.LeaseToken, "invalid set_task_state payload: "+err.Error(), false)
		if failErr != nil {
			return nil, failErr
		}
		return &EffectDelivery{Effect: failed, ExitCode: 1}, err
	}
	target, _ := spec.Data["state"].(string)
	target = strings.TrimSpace(target)
	targetState, err := domain.ParseState(target)
	if err != nil {
		failed, failErr := s.FailEffect(eff.ID, claim.LeaseToken, err.Error(), false)
		if failErr != nil {
			return nil, failErr
		}
		return &EffectDelivery{Effect: failed, ExitCode: 1, Stderr: err.Error()}, err
	}
	inst, err := s.instanceByID(eff.InstanceID)
	if err != nil {
		return nil, err
	}
	before, err := loadTaskDoc(s.db, inst.TaskUUID)
	if err != nil {
		return nil, err
	}
	newETag := before.ETag
	target = string(targetState)
	alreadyApplied := before.State == target
	if !alreadyApplied {
		newETag, err = store.New(s.db).Tasks.UpdateFieldsWithViaAttribution(workflowSystemAttribution, inst.TaskUUID, map[string]interface{}{"state": target}, before.ETag, "wrkf.effect:set_task_state")
		if err != nil {
			failed, failErr := s.FailEffect(eff.ID, claim.LeaseToken, err.Error(), true)
			if failErr != nil {
				return nil, failErr
			}
			return &EffectDelivery{Effect: failed, ExitCode: 1, Stderr: err.Error()}, err
		}
	}
	receiptMap := map[string]interface{}{
		"kind":           "set_task_state.receipt",
		"taskUuid":       inst.TaskUUID,
		"taskRef":        inst.TaskRef,
		"from":           before.State,
		"to":             target,
		"previousEtag":   before.ETag,
		"newEtag":        newETag,
		"alreadyApplied": alreadyApplied,
		"effectId":       eff.ID,
		"semanticKey":    eff.SemanticKey,
	}
	receipt, _ := json.Marshal(receiptMap)
	delivered, err := s.AckEffectWithReceipt(eff.ID, claim.LeaseToken, json.RawMessage(receipt))
	if err != nil {
		return nil, err
	}
	return &EffectDelivery{Effect: delivered, Receipt: json.RawMessage(receipt), ExitCode: 0, Stdout: string(receipt)}, nil
}

func (s *Service) deliverBuiltinTransitionEffects(result map[string]interface{}, transitionID string) (map[string]interface{}, error) {
	if result == nil {
		return result, nil
	}

	effects, err := transitionResultEffects(result["effects"])
	if err != nil {
		return result, err
	}
	if len(effects) == 0 {
		return result, nil
	}

	eventID, _ := result["eventId"].(string)
	for i := range effects {
		if !isEngineOwnedBuiltinEffect(effects[i].Kind) {
			continue
		}
		effectID := effects[i].ID
		delivery, deliverErr := s.DeliverEffect(effectID, transitionBuiltinEffectAdapter, nil, "")
		current := &effects[i]
		if delivery != nil && delivery.Effect != nil {
			current = delivery.Effect
		} else if shown, showErr := s.ShowEffect(effectID); showErr == nil {
			current = shown
		}
		effects[i] = *current
		result["effects"] = effects

		if deliverErr != nil || current.Status != "delivered" {
			cause := deliverErr
			if cause == nil {
				cause = fmt.Errorf("builtin effect %s finished with status %s", current.ID, current.Status)
			}
			return result, &transitionEffectDeliveryError{
				transitionID: transitionID,
				eventID:      eventID,
				effectID:     current.ID,
				kind:         current.Kind,
				status:       current.Status,
				err:          cause,
				result:       result,
			}
		}
	}
	return result, nil
}

func transitionResultEffects(v interface{}) ([]Effect, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []Effect:
		out := make([]Effect, len(x))
		copy(out, x)
		return out, nil
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, err
		}
		var out []Effect
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// workflowSystemAttribution is the principal-only attribution the wrkf engine
// uses for its own system writes (e.g. set_task_state effects).
var workflowSystemAttribution = attribution.Attribution{PrincipalRef: "agent:wrkf-system"}

func (s *Service) latestRunForRole(instanceID, role string) (*Run, error) {
	runs, err := queryRuns(s.db, `
		WHERE instance_id = ? AND role = ? AND status = 'active' AND COALESCE(delivery_ref,'') != ''
		ORDER BY started_at DESC, id DESC LIMIT 1
	`, instanceID, role)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("role %s is not bound; run wrkf run bind TASK %s HANDLE", role, role)
	}
	return &runs[0], nil
}

func effectRole(eff *Effect) string {
	if eff == nil || len(eff.Payload) == 0 {
		return ""
	}
	var payload struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(eff.Payload, &payload)
	return payload.Role
}
