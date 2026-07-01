package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ActionNext returns machine-readable executable-action candidates for an
// already-attached v2 workflow instance. It is deliberately read-only: missing
// workflows, legacy v1 templates, and nonmatching states produce no candidates
// rather than installing or attaching anything.
func (s *Service) ActionNext(p ActionNextParams) (*ActionNextResult, error) {
	inst, err := s.actionNextInstance(p)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return &ActionNextResult{Candidates: []ActionCandidate{}}, nil
	}
	return s.actionCandidatesForInstance(s.db, inst, p)
}

type actionCandidateQueryer interface {
	queryer
	rowsQueryer
}

func (s *Service) actionCandidatesForInstance(q actionCandidateQueryer, inst *Instance, p ActionNextParams) (*ActionNextResult, error) {
	if !templateAllowedByScope(inst, p.Scope) {
		return &ActionNextResult{Candidates: []ActionCandidate{}}, nil
	}
	if !matchesAnyFilter(inst.Status, p.Filters.Statuses) || !matchesAnyFilter(inst.Phase, p.Filters.Phases) {
		return &ActionNextResult{Candidates: []ActionCandidate{}}, nil
	}
	tpl, _, err := showTemplateTx(q, inst.TemplateID+"@"+inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	if len(tpl.ExecutableActions) == 0 {
		return &ActionNextResult{Candidates: []ActionCandidate{}}, nil
	}
	task, err := loadTaskDoc(q, inst.TaskUUID)
	if err != nil {
		return nil, err
	}
	ev, err := listEvidenceForInstanceRows(q, inst.ID)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(tpl.ExecutableActions))
	for id := range tpl.ExecutableActions {
		ids = append(ids, id)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		left, right := tpl.ExecutableActions[ids[i]], tpl.ExecutableActions[ids[j]]
		if actionRank(left, i) != actionRank(right, j) {
			return actionRank(left, i) < actionRank(right, j)
		}
		return ids[i] < ids[j]
	})

	limit := p.Limit
	if limit <= 0 || limit > actionListMaxLimit {
		limit = actionListMaxLimit
	}
	candidates := []ActionCandidate{}
	for idx, actionID := range ids {
		spec := tpl.ExecutableActions[actionID]
		if !matchesAnyFilter(actionID, p.Filters.Actions) || !matchesAnyFilter(spec.Role, p.Filters.Roles) {
			continue
		}
		candidate, blockedReason, err := candidateForExecutableAction(q, inst, task, tpl, ev, actionID, spec, idx)
		if err != nil {
			return nil, err
		}
		if blockedReason != "" {
			if !p.Filters.IncludeBlocked {
				continue
			}
			candidate.Blocked = true
			candidate.BlockedReason = blockedReason
		}
		candidates = append(candidates, candidate)
		if len(candidates) >= limit {
			break
		}
	}
	return &ActionNextResult{Candidates: candidates}, nil
}

func (s *Service) actionNextInstance(p ActionNextParams) (*Instance, error) {
	task := strings.TrimSpace(p.Task)
	if task == "" {
		task = strings.TrimSpace(p.Scope.Path)
	}
	instanceID := strings.TrimSpace(p.InstanceID)
	if task == "" && instanceID == "" {
		return nil, validationError("selector", "task, instanceId, or scope.path is required", "task or instanceId", nil, "supply a task selector or instanceId")
	}
	inst, err := s.ResolveInstance(task, instanceID)
	if err != nil {
		if instanceID == "" && strings.Contains(err.Error(), "workflow instance not found") {
			return nil, nil
		}
		return nil, err
	}
	return inst, nil
}

func templateAllowedByScope(inst *Instance, scope ActionNextScope) bool {
	if len(scope.Templates) == 0 {
		return true
	}
	ref := inst.TemplateID + "@" + inst.TemplateVersion
	for _, allowed := range scope.Templates {
		if strings.TrimSpace(allowed) == ref || strings.TrimSpace(allowed) == inst.TemplateID {
			return true
		}
	}
	return false
}

func candidateForExecutableAction(q queryer, inst *Instance, task *taskDoc, tpl *Template, ev []Evidence, actionID string, spec ExecutableActionSpec, idx int) (ActionCandidate, string, error) {
	transitionID := strings.TrimSpace(spec.Transition)
	tr, err := findTransition(tpl, transitionID)
	if err != nil {
		return ActionCandidate{}, "", err
	}
	from := &tr.From
	if spec.From != nil {
		from = spec.From
	}
	if !stateMatches(*inst, *from) {
		return actionCandidateBase(inst, task, actionID, spec, nil, actionRank(spec, idx)), fmt.Sprintf("instance state is %s; action requires %s", stateKey(inst.State()), stateKey(*from)), nil
	}

	var source *ActionSourceBinding
	if spec.SourceBinding != nil {
		var blocked string
		source, blocked = resolveActionCandidateSource(q, tpl, ev, spec.SourceBinding)
		if blocked != "" {
			return actionCandidateBase(inst, task, actionID, spec, nil, actionRank(spec, idx)), blocked, nil
		}
	}
	candidate := actionCandidateBase(inst, task, actionID, spec, source, actionRank(spec, idx))
	candidate.SemanticActionKey = semanticActionKey(inst, actionID, source)
	candidate.InputHash = actionCandidateInputHash(candidate)
	return candidate, "", nil
}

func actionCandidateBase(inst *Instance, task *taskDoc, actionID string, spec ExecutableActionSpec, source *ActionSourceBinding, rank int) ActionCandidate {
	taskHash := inst.TaskDocHash
	if task != nil {
		taskHash = taskDocHash(task)
	}
	candidate := ActionCandidate{
		InstanceID:            inst.ID,
		Task:                  strings.TrimPrefix(inst.TaskRef, "wrkq:"),
		Action:                actionID,
		Transition:            spec.Transition,
		Role:                  spec.Role,
		RequiredEvidenceKind:  spec.ResultEvidenceKind,
		ExpectedStateRevision: inst.Revision,
		ExpectedState:         inst.State(),
		ExpectedTaskDocHash:   taskHash,
		Source:                source,
		HandlerContract:       spec.HandlerContract,
		WorkspaceMode:         spec.WorkspaceMode,
		SideEffectClasses:     append([]string(nil), spec.SideEffectClasses...),
		Rank:                  rank,
	}
	candidate.SemanticActionKey = semanticActionKey(inst, actionID, source)
	if candidate.SemanticActionKey != "" {
		candidate.InputHash = actionCandidateInputHash(candidate)
	}
	return candidate
}

func resolveActionCandidateSource(q queryer, tpl *Template, ev []Evidence, binding *SourceBindingSpec) (*ActionSourceBinding, string) {
	sourceAction := strings.TrimSpace(binding.Action)
	sourceSpec, ok := tpl.ExecutableActions[sourceAction]
	if !ok {
		return nil, fmt.Sprintf("source action %s is not executable", sourceAction)
	}
	required := compactStrings(binding.RequiredFacts)
	for i := len(ev) - 1; i >= 0; i-- {
		e := ev[i]
		if e.Kind != sourceSpec.ResultEvidenceKind || e.RunID == "" {
			continue
		}
		runAction, err := runActionByIDQuery(q, e.RunID)
		if err != nil || runAction != sourceAction {
			continue
		}
		facts := evidenceFactsMap(e)
		if missing := missingRequiredFacts(facts, required); len(missing) > 0 {
			continue
		}
		commit := stringFact(facts, "commit.sha")
		artifact := stringFact(facts, "artifact.digest")
		if artifact == "" {
			artifact = e.ContentHash
		}
		if commit == "" && artifact == "" {
			return nil, fmt.Sprintf("source evidence %s lacks commit.sha or artifact.digest", e.ID)
		}
		return &ActionSourceBinding{
			SourceRunID:      e.RunID,
			SourceEvidenceID: e.ID,
			CommitSha:        commit,
			ArtifactRef:      artifact,
		}, ""
	}
	if len(required) > 0 {
		return nil, fmt.Sprintf("source binding %s evidence with required facts %s is missing", sourceSpec.ResultEvidenceKind, strings.Join(required, ","))
	}
	return nil, fmt.Sprintf("source binding %s evidence is missing", sourceSpec.ResultEvidenceKind)
}

func listEvidenceForInstanceRows(q rowsQueryer, instanceID string) ([]Evidence, error) {
	rows, err := q.Query(`
		SELECT id, instance_id, kind, ref, COALESCE(summary,''), COALESCE(facts_json,''), COALESCE(data_json,''), source_json,
		       COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''), COALESCE(task_etag_at_production,''), COALESCE(task_hash_at_production,''), produced_at
		FROM workflow_evidence WHERE instance_id = ? ORDER BY produced_at, id
	`, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEvidenceRows(rows)
}

func runActionByIDQuery(q queryer, runID string) (string, error) {
	var action string
	err := q.QueryRow(`SELECT COALESCE(action,'') FROM workflow_runs WHERE id = ?`, runID).Scan(&action)
	return action, err
}

func semanticActionKey(inst *Instance, actionID string, source *ActionSourceBinding) string {
	if source != nil {
		identity := source.CommitSha
		if identity == "" {
			identity = source.ArtifactRef
		}
		return fmt.Sprintf("%s:%s:%s:%s", actionID, inst.ID, source.SourceRunID, identity)
	}
	return fmt.Sprintf("%s:%s:r%d", actionID, inst.ID, inst.Revision)
}

func actionCandidateInputHash(candidate ActionCandidate) string {
	payload := map[string]interface{}{
		"instanceId":            candidate.InstanceID,
		"semanticActionKey":     candidate.SemanticActionKey,
		"action":                candidate.Action,
		"transition":            candidate.Transition,
		"role":                  candidate.Role,
		"expectedStateRevision": candidate.ExpectedStateRevision,
		"expectedTaskDocHash":   candidate.ExpectedTaskDocHash,
		"source":                candidate.Source,
	}
	b, _ := json.Marshal(payload)
	return Hash(b)
}

func actionRank(spec ExecutableActionSpec, index int) int {
	if spec.Rank != 0 {
		return spec.Rank
	}
	return 100 + index
}

func evidenceFactsMap(e Evidence) map[string]interface{} {
	out := map[string]interface{}{}
	if len(e.Facts) == 0 {
		return out
	}
	_ = json.Unmarshal(e.Facts, &out)
	return out
}

func missingRequiredFacts(facts map[string]interface{}, required []string) []string {
	var missing []string
	for _, key := range required {
		if stringFact(facts, key) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func stringFact(facts map[string]interface{}, key string) string {
	if facts == nil {
		return ""
	}
	v, ok := facts[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func matchesAnyFilter(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	value = strings.TrimSpace(value)
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}
