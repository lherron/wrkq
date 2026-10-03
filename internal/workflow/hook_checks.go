//go:build wrkq_local

package workflow

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (o HookExecutionOptions) context() context.Context {
	if o.Context != nil {
		return o.Context
	}
	return context.Background()
}

func (s *Service) RunChecks(taskSelector, transitionID, actor, role string, catalog *HookCatalog, templateDir string) ([]CheckRun, error) {
	return s.RunChecksWithOptions(taskSelector, transitionID, actor, role, catalog, templateDir, HookExecutionOptions{})
}

func (s *Service) RunChecksWithOptions(taskSelector, transitionID, actor, role string, catalog *HookCatalog, templateDir string, execOpts HookExecutionOptions) ([]CheckRun, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	tr, err := findTransition(tpl, transitionID)
	if err != nil {
		return nil, err
	}
	// Refuse an oversized remote catalog before the first hook runs or any
	// check result is persisted. This makes the request all-or-nothing with
	// respect to timeout policy even when a transition declares many checks.
	var pinned *HookCatalog
	for _, checkID := range tr.Checks {
		check, ok := tpl.Checks[checkID]
		if !ok {
			return nil, fmt.Errorf("check not found: %s", checkID)
		}
		if check.Type != "hook" {
			continue
		}
		if pinned == nil {
			pinned, err = s.pinnedHookCatalog(inst.TemplateID, inst.TemplateVersion, catalog)
			if err != nil {
				return nil, err
			}
		}
		hook, ok := pinned.Hooks[check.HookID]
		if !ok {
			return nil, fmt.Errorf("hook not found: %s", check.HookID)
		}
		if _, err := effectiveHookTimeout(hook, execOpts.TimeoutCeiling); err != nil {
			return nil, err
		}
	}
	var out []CheckRun
	for _, checkID := range tr.Checks {
		check, ok := tpl.Checks[checkID]
		if !ok {
			return nil, fmt.Errorf("check not found: %s", checkID)
		}
		cr, err := s.executeCheck(inst, tr, checkID, check, actor, role, catalog, templateDir, true, execOpts)
		if err != nil {
			return nil, err
		}
		out = append(out, *cr)
	}
	return out, nil
}

func (s *Service) RunSingleHook(taskSelector, transitionID, hookID, actor, role string, catalog *HookCatalog, templateDir string) (*CheckRun, error) {
	return s.RunSingleHookWithOptions(taskSelector, transitionID, hookID, actor, role, catalog, templateDir, HookExecutionOptions{})
}

func (s *Service) RunSingleHookWithOptions(taskSelector, transitionID, hookID, actor, role string, catalog *HookCatalog, templateDir string, execOpts HookExecutionOptions) (*CheckRun, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	pinned, err := s.pinnedHookCatalog(inst.TemplateID, inst.TemplateVersion, catalog)
	if err != nil {
		return nil, err
	}
	hook, ok := pinned.Hooks[hookID]
	if !ok {
		return nil, fmt.Errorf("hook not found: %s", hookID)
	}
	if _, err := effectiveHookTimeout(hook, execOpts.TimeoutCeiling); err != nil {
		return nil, err
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	tr, err := findTransition(tpl, transitionID)
	if err != nil {
		return nil, err
	}
	check := CheckSpec{Type: "hook", HookID: hookID, ExitMap: map[string]ExitMap{"0": {Verdict: "pass", Outcome: "passed"}, "*": {Verdict: "error", Outcome: "failed"}}}
	return s.executeCheck(inst, tr, hookID, check, actor, role, catalog, templateDir, false, execOpts)
}

func buildCheckInput(inst *Instance, tr *TransitionSpec, actor, role string, task *taskDoc, ev []Evidence, obl []Obligation) ([]byte, map[string]interface{}) {
	facts := taskFacts(task)
	taskHash := ""
	taskEtag := ""
	if task != nil {
		taskHash = taskDocHash(task)
		taskEtag = fmt.Sprint(task.ETag)
	}
	input := map[string]interface{}{
		"task":          map[string]interface{}{"ref": inst.TaskRef, "uuid": inst.TaskUUID, "etag": taskEtag, "hash": taskHash},
		"workflow":      map[string]interface{}{"instanceId": inst.ID, "state": inst.State(), "revision": inst.Revision},
		"transition":    map[string]interface{}{"id": tr.ID},
		"principal_ref": map[string]interface{}{"id": actor},
		"role":          role,
		"facts":         facts,
		"evidence":      ev,
		"obligations":   obl,
	}
	inputJSON, _ := json.Marshal(input)
	return inputJSON, facts
}

func currentCheckInputHash(inst *Instance, tr *TransitionSpec, actor, role string, task *taskDoc, ev []Evidence, obl []Obligation) string {
	inputJSON, _ := buildCheckInput(inst, tr, actor, role, task, ev, obl)
	return Hash(inputJSON)
}

func (s *Service) executeCheck(inst *Instance, tr *TransitionSpec, checkID string, check CheckSpec, actor, role string, catalog *HookCatalog, templateDir string, persist bool, execOpts HookExecutionOptions) (*CheckRun, error) {
	task, _ := loadTaskDoc(s.db, inst.TaskUUID)
	ev, _ := listEvidenceForInstance(s.db, inst.ID)
	obl, _ := listObligationsForInstance(s.db, inst.ID, true)
	inputJSON, facts := buildCheckInput(inst, tr, actor, role, task, ev, obl)
	cr := &CheckRun{InstanceID: inst.ID, TransitionID: tr.ID, CheckID: checkID, HookID: check.HookID, InputHash: Hash(inputJSON), Verdict: "inconclusive", PrincipalRef: actor, Role: role, StartedAt: s.now().Format(time.RFC3339)}
	switch check.Type {
	case "predicate":
		if check.Predicate == nil {
			cr.Verdict = "error"
			cr.Summary = "predicate check missing predicate"
		} else if evalPredicate(*check.Predicate, evalContext{Evidence: ev, Obligations: obl, Checks: map[string]CheckRun{}, Facts: facts, Task: task, State: inst.State()}) {
			cr.Verdict = "pass"
			cr.Outcome = "passed"
		} else {
			cr.Verdict = "fail"
			cr.Outcome = "failed"
		}
	case "builtin":
		switch check.Name {
		case "always_pass":
			cr.Verdict = "pass"
			cr.Outcome = "passed"
		case "always_fail":
			cr.Verdict = "fail"
			cr.Outcome = "failed"
		case "task_completed":
			if task != nil && task.State == "completed" {
				cr.Verdict = "pass"
				cr.Outcome = "completed"
			} else {
				cr.Verdict = "fail"
				cr.Outcome = "not_completed"
			}
		default:
			cr.Verdict = "error"
			cr.Outcome = "unknown_builtin"
			cr.Summary = "unknown builtin check"
		}
	case "hook":
		pinned, err := s.pinnedHookCatalog(inst.TemplateID, inst.TemplateVersion, catalog)
		if err != nil {
			return nil, err
		}
		hook, ok := pinned.Hooks[check.HookID]
		if !ok {
			return nil, fmt.Errorf("hook not found: %s", check.HookID)
		}
		execCtx := execOpts.context()
		exit, stdout, stderr, err := runHook(execCtx, hook, templateDir, inputJSON, execOpts.TimeoutCeiling)
		cr.ExitCode = &exit
		if ctxErr := execCtx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil && exit == -1 {
			cr.Verdict = "error"
			cr.Outcome = "hook_error"
			cr.Summary = err.Error()
		} else if hook.Stdout == "json" && len(bytes.TrimSpace(stdout)) > 0 {
			var doc struct {
				Verdict string                 `json:"verdict"`
				Code    string                 `json:"code"`
				Summary string                 `json:"summary"`
				Facts   map[string]interface{} `json:"facts"`
			}
			if err := json.Unmarshal(stdout, &doc); err == nil && doc.Verdict != "" {
				cr.Verdict = doc.Verdict
				cr.Code = doc.Code
				cr.Summary = doc.Summary
				if doc.Facts != nil {
					cr.Facts, _ = json.Marshal(doc.Facts)
				}
			}
		} else {
			m := check.ExitMap[fmt.Sprint(exit)]
			if m.Verdict == "" {
				m = check.ExitMap["*"]
			}
			if m.Verdict == "" {
				if exit == 0 {
					m.Verdict = "pass"
				} else {
					m.Verdict = "error"
				}
			}
			cr.Verdict = m.Verdict
			cr.Outcome = m.Outcome
			if len(stderr) > 0 {
				cr.Summary = strings.TrimSpace(string(stderr))
			}
		}
	case "role":
		cr.Verdict = "inconclusive"
		cr.Outcome = "role_required"
		cr.Summary = check.Instruction
	default:
		cr.Verdict = "error"
		cr.Outcome = "unknown_check_type"
	}
	cr.CompletedAt = s.now().Format(time.RFC3339)
	if persist {
		if err := withTx(s.db.DB, func(tx *sql.Tx) error {
			id, err := nextSeqID(tx, "workflow_check_run_seq", "chk")
			if err != nil {
				return err
			}
			cr.ID = id
			var facts interface{}
			if len(cr.Facts) > 0 {
				facts = string(cr.Facts)
			}
			_, err = tx.Exec(`
				INSERT INTO workflow_check_runs (
					id, instance_id, transition_id, check_id, hook_id, input_hash, exit_code, verdict,
					outcome, code, summary, facts_json, actor, principal_ref, role, started_at, completed_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, cr.ID, cr.InstanceID, cr.TransitionID, cr.CheckID, nullIfEmpty(cr.HookID), cr.InputHash, cr.ExitCode, cr.Verdict, nullIfEmpty(cr.Outcome), nullIfEmpty(cr.Code), nullIfEmpty(cr.Summary), facts, emptyToNil(cr.PrincipalRef), emptyToNil(cr.PrincipalRef), emptyToNil(cr.Role), cr.StartedAt, cr.CompletedAt)
			return err
		}); err != nil {
			return nil, err
		}
	}
	return cr, nil
}

func taskFacts(task *taskDoc) map[string]interface{} {
	if task == nil || strings.TrimSpace(task.Meta) == "" {
		return map[string]interface{}{}
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(task.Meta), &meta); err != nil {
		return map[string]interface{}{}
	}
	facts, ok := meta["workflowFacts"].(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return facts
}

func effectiveHookTimeout(hook HookSpec, ceiling time.Duration) (time.Duration, error) {
	timeout := time.Duration(hook.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if ceiling > 0 && timeout > ceiling {
		return 0, validationError(
			"timeoutMs",
			fmt.Sprintf("hook timeout %s exceeds remote execution ceiling %s", timeout, ceiling),
			fmt.Sprintf("duration no greater than %s", ceiling),
			nil,
			"reduce timeoutMs in the deployed hook catalog",
		)
	}
	return timeout, nil
}

func runHook(parent context.Context, hook HookSpec, templateDir string, input []byte, ceiling time.Duration) (int, []byte, []byte, error) {
	if len(hook.Argv) == 0 {
		return -1, nil, nil, fmt.Errorf("hook argv is empty")
	}
	timeout, err := effectiveHookTimeout(hook, ceiling)
	if err != nil {
		return -1, nil, nil, err
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, hook.Argv[0], hook.Argv[1:]...)
	if hook.CWD == "template_dir" && templateDir != "" {
		cmd.Dir = templateDir
	} else if hook.CWD != "" && hook.CWD != "template_dir" {
		cmd.Dir = hook.CWD
	}
	if hook.Stdin == "json" {
		cmd.Stdin = bytes.NewReader(input)
	}
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return -1, stdout.Bytes(), stderr.Bytes(), err
		}
	}
	return exitCode, limitBytes(stdout.Bytes(), hook.MaxStdoutBytes), limitBytes(stderr.Bytes(), hook.MaxStderrBytes), err
}

func limitBytes(b []byte, max int) []byte {
	if max <= 0 || len(b) <= max {
		return b
	}
	return b[:max]
}

func LoadHookCatalog(path string) (*HookCatalog, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, ErrHookCatalogNotConfigured
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cat HookCatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, err
	}
	if cat.Hooks == nil {
		cat.Hooks = map[string]HookSpec{}
	}
	if cat.EffectHandlers == nil {
		cat.EffectHandlers = map[string]HookSpec{}
	}
	return &cat, nil
}

var ErrHookCatalogNotConfigured = errors.New(
	"hook catalog configuration is required; set --hook-catalog PATH or WRKF_HOOK_CATALOG=PATH",
)

// ConfiguredHookCatalogPath returns the caller-selected catalog path. Catalogs
// are workflow law, so resolution is deliberately limited to explicit
// configuration and never consults cwd, ancestor directories, room state, or a
// user's home directory.
func ConfiguredHookCatalogPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return strings.TrimSpace(path), nil
	}
	if envPath := strings.TrimSpace(os.Getenv("WRKF_HOOK_CATALOG")); envPath != "" {
		return envPath, nil
	}
	return "", ErrHookCatalogNotConfigured
}

func HookCatalogDir(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}
