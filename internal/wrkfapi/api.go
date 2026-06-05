package wrkfapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/lherron/wrkq/internal/workflow"
)

type API struct {
	service     *workflow.Service
	hookCatalog *workflow.HookCatalog
	templateDir string
}

type Option func(*API)

func New(service *workflow.Service, opts ...Option) *API {
	api := &API{service: service}
	for _, opt := range opts {
		opt(api)
	}
	return api
}

func WithHookCatalog(catalog *workflow.HookCatalog) Option {
	return func(api *API) {
		api.hookCatalog = catalog
	}
}

func WithTemplateDir(dir string) Option {
	return func(api *API) {
		api.templateDir = dir
	}
}

func (api *API) WorkflowValidate(ctx context.Context, path string) (workflow.ValidateResult, error) {
	if err := ctx.Err(); err != nil {
		return workflow.ValidateResult{}, err
	}
	return api.service.ValidateTemplateFile(path, api.hookCatalog), nil
}

func (api *API) ValidateTemplate(ctx context.Context, path string) (workflow.ValidateResult, error) {
	return api.WorkflowValidate(ctx, path)
}

func (api *API) WorkflowShow(ctx context.Context, ref string) (WorkflowShowResult, error) {
	if err := ctx.Err(); err != nil {
		return WorkflowShowResult{}, err
	}
	tpl, hash, err := api.service.ShowTemplate(ref)
	if err != nil {
		return WorkflowShowResult{}, normalizeError(err)
	}
	return WorkflowShowResult{Template: *tpl, Hash: hash}, nil
}

func (api *API) ShowTemplate(ctx context.Context, ref string) (WorkflowShowResult, error) {
	return api.WorkflowShow(ctx, ref)
}

func (api *API) WorkflowList(ctx context.Context) (WorkflowListResult, error) {
	return api.ListTemplates(ctx)
}

func (api *API) ListTemplates(ctx context.Context) (WorkflowListResult, error) {
	if err := ctx.Err(); err != nil {
		return WorkflowListResult{}, err
	}
	rows, err := api.service.ListTemplates()
	if err != nil {
		return WorkflowListResult{}, normalizeError(err)
	}
	templates := make([]TemplateSummary, 0, len(rows))
	for _, row := range rows {
		templates = append(templates, templateSummaryFromAny(row))
	}
	return WorkflowListResult{Templates: templates}, nil
}

func (api *API) WorkflowDiff(ctx context.Context, oldPath, newPath string) (DiffResult, error) {
	if err := ctx.Err(); err != nil {
		return DiffResult{}, err
	}
	out, err := api.service.DiffTemplateFiles(oldPath, newPath)
	if err != nil {
		return DiffResult{}, normalizeError(err)
	}
	return DiffResult{
		Old:      templateSummaryFromAny(out["old"]),
		New:      templateSummaryFromAny(out["new"]),
		SameHash: boolFromAny(out["sameHash"]),
	}, nil
}

func (api *API) Diff(ctx context.Context, oldPath, newPath string) (DiffResult, error) {
	return api.WorkflowDiff(ctx, oldPath, newPath)
}

func (api *API) WorkflowInstall(ctx context.Context, path, actor string) (InstallResult, error) {
	if err := ctx.Err(); err != nil {
		return InstallResult{}, err
	}
	out, err := api.service.InstallTemplate(path, actor, api.hookCatalog)
	if err != nil {
		return InstallResult{}, normalizeError(err)
	}
	return InstallResult{
		ID:        stringFromAny(out["id"]),
		Version:   stringFromAny(out["version"]),
		Hash:      stringFromAny(out["hash"]),
		Installed: boolFromAny(out["installed"]),
	}, nil
}

func (api *API) InstallTemplate(ctx context.Context, path, actor string) (InstallResult, error) {
	return api.WorkflowInstall(ctx, path, actor)
}

func (api *API) TaskAttach(ctx context.Context, taskSelector, templateRef, actor string) (*workflow.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inst, err := api.service.AttachTask(taskSelector, templateRef, actor)
	if err != nil {
		return nil, normalizeError(err)
	}
	return inst, nil
}

func (api *API) AttachTask(ctx context.Context, taskSelector, templateRef, actor string) (*workflow.Instance, error) {
	return api.TaskAttach(ctx, taskSelector, templateRef, actor)
}

func (api *API) TaskInspect(ctx context.Context, taskSelector string) (*workflow.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inst, err := api.service.InspectTask(taskSelector)
	if err != nil {
		return nil, normalizeError(err)
	}
	return inst, nil
}

func (api *API) TaskTimeline(ctx context.Context, taskSelector string) ([]workflow.Event, error) {
	return api.Timeline(ctx, taskSelector)
}

func (api *API) Timeline(ctx context.Context, taskSelector string) ([]workflow.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	events, err := api.service.Timeline(taskSelector)
	if err != nil {
		return nil, normalizeError(err)
	}
	return events, nil
}

func (api *API) TaskRefresh(ctx context.Context, taskSelector, actor string) (*workflow.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inst, err := api.service.Refresh(taskSelector, actor)
	if err != nil {
		return nil, normalizeError(err)
	}
	return inst, nil
}

func (api *API) Refresh(ctx context.Context, taskSelector, actor string) (*workflow.Instance, error) {
	return api.TaskRefresh(ctx, taskSelector, actor)
}

func (api *API) TaskSyncMeta(ctx context.Context, taskSelector, actor string) (SyncMetaResult, error) {
	if err := ctx.Err(); err != nil {
		return SyncMetaResult{}, err
	}
	updated, err := api.service.SyncMeta(taskSelector, actor)
	if err != nil {
		return SyncMetaResult{}, normalizeError(err)
	}
	return SyncMetaResult{Updated: updated}, nil
}

func (api *API) SyncMeta(ctx context.Context, taskSelector, actor string) (SyncMetaResult, error) {
	return api.TaskSyncMeta(ctx, taskSelector, actor)
}

func (api *API) Next(ctx context.Context, taskSelector, role string) (*workflow.NextActionResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := api.service.Next(taskSelector, role)
	if err != nil {
		return nil, normalizeError(err)
	}
	return resp, nil
}

func (api *API) EvidenceAdd(ctx context.Context, params EvidenceAddParams) (*workflow.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ev, err := api.service.AddEvidence(params.workflowParams())
	if err != nil {
		return nil, normalizeError(err)
	}
	return ev, nil
}

func (api *API) AddEvidence(ctx context.Context, params EvidenceAddParams) (*workflow.Evidence, error) {
	return api.EvidenceAdd(ctx, params)
}

func (api *API) EvidenceList(ctx context.Context, taskSelector string) ([]workflow.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ev, err := api.service.ListEvidence(taskSelector)
	if err != nil {
		return nil, normalizeError(err)
	}
	return ev, nil
}

func (api *API) ListEvidence(ctx context.Context, taskSelector string) ([]workflow.Evidence, error) {
	return api.EvidenceList(ctx, taskSelector)
}

func (api *API) EvidenceShow(ctx context.Context, id string) (*workflow.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ev, err := api.service.ShowEvidence(id)
	if err != nil {
		return nil, normalizeError(err)
	}
	return ev, nil
}

func (api *API) ShowEvidence(ctx context.Context, id string) (*workflow.Evidence, error) {
	return api.EvidenceShow(ctx, id)
}

func (api *API) EvidenceSuggest(ctx context.Context, taskSelector, transition string) (SuggestResult, error) {
	return api.Suggest(ctx, taskSelector, transition)
}

func (api *API) Suggest(ctx context.Context, taskSelector, transition string) (SuggestResult, error) {
	if err := ctx.Err(); err != nil {
		return SuggestResult{}, err
	}
	out, err := api.service.SuggestEvidence(taskSelector, transition)
	if err != nil {
		return SuggestResult{}, normalizeError(err)
	}
	return suggestFromAny(out), nil
}

func (api *API) ObligationList(ctx context.Context, taskSelector string, includeClosed bool) ([]workflow.Obligation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obl, err := api.service.ListObligations(taskSelector, includeClosed)
	if err != nil {
		return nil, normalizeError(err)
	}
	return obl, nil
}

func (api *API) ListObligations(ctx context.Context, taskSelector string, includeClosed bool) ([]workflow.Obligation, error) {
	return api.ObligationList(ctx, taskSelector, includeClosed)
}

func (api *API) ObligationShow(ctx context.Context, id string) (*workflow.Obligation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obl, err := api.service.ShowObligation(id)
	if err != nil {
		return nil, normalizeError(err)
	}
	return obl, nil
}

func (api *API) ShowObligation(ctx context.Context, id string) (*workflow.Obligation, error) {
	return api.ObligationShow(ctx, id)
}

func (api *API) ObligationSatisfy(ctx context.Context, params ObligationStatusParams) (*workflow.Obligation, error) {
	return api.setObligationStatus(ctx, params, "satisfied")
}

func (api *API) SatisfyObligation(ctx context.Context, params ObligationStatusParams) (*workflow.Obligation, error) {
	return api.ObligationSatisfy(ctx, params)
}

func (api *API) ObligationWaive(ctx context.Context, params ObligationStatusParams) (*workflow.Obligation, error) {
	return api.setObligationStatus(ctx, params, "waived")
}

func (api *API) WaiveObligation(ctx context.Context, params ObligationStatusParams) (*workflow.Obligation, error) {
	return api.ObligationWaive(ctx, params)
}

func (api *API) ObligationCancel(ctx context.Context, params ObligationStatusParams) (*workflow.Obligation, error) {
	return api.setObligationStatus(ctx, params, "cancelled")
}

func (api *API) CancelObligation(ctx context.Context, params ObligationStatusParams) (*workflow.Obligation, error) {
	return api.ObligationCancel(ctx, params)
}

func (api *API) CheckPreflight(ctx context.Context, taskSelector, transition, role string) (*workflow.NextActionResponse, error) {
	return api.Next(ctx, taskSelector, role)
}

func (api *API) Preflight(ctx context.Context, taskSelector, transition, role string) (*workflow.NextActionResponse, error) {
	return api.CheckPreflight(ctx, taskSelector, transition, role)
}

func (api *API) CheckRun(ctx context.Context, params CheckRunParams) (CheckRunResult, error) {
	if err := ctx.Err(); err != nil {
		return CheckRunResult{}, err
	}
	runs, err := api.service.RunChecks(params.TaskSelector, params.Transition, params.Actor, params.Role, api.hookCatalog, api.templateDir)
	if err != nil {
		return CheckRunResult{}, normalizeError(err)
	}
	return CheckRunResult{Runs: runs}, nil
}

func (api *API) RunChecks(ctx context.Context, params CheckRunParams) (CheckRunResult, error) {
	return api.CheckRun(ctx, params)
}

func (api *API) CheckShow(ctx context.Context, id string) (*workflow.CheckRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := api.service.ShowCheckRun(id)
	if err != nil {
		return nil, normalizeError(err)
	}
	return run, nil
}

func (api *API) ShowCheckRun(ctx context.Context, id string) (*workflow.CheckRun, error) {
	return api.CheckShow(ctx, id)
}

func (api *API) CheckList(ctx context.Context, taskSelector, transition string) ([]workflow.CheckRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runs, err := api.service.ListCheckRuns(taskSelector, transition)
	if err != nil {
		return nil, normalizeError(err)
	}
	return runs, nil
}

func (api *API) ListCheckRuns(ctx context.Context, taskSelector, transition string) ([]workflow.CheckRun, error) {
	return api.CheckList(ctx, taskSelector, transition)
}

func (api *API) EffectList(ctx context.Context, taskSelector string, all bool) ([]workflow.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	effects, err := api.service.ListEffects(taskSelector, all)
	if err != nil {
		return nil, normalizeError(err)
	}
	return effects, nil
}

func (api *API) ListEffects(ctx context.Context, taskSelector string, all bool) ([]workflow.Effect, error) {
	return api.EffectList(ctx, taskSelector, all)
}

func (api *API) EffectShow(ctx context.Context, id string) (*workflow.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	effect, err := api.service.ShowEffect(id)
	if err != nil {
		return nil, normalizeError(err)
	}
	return effect, nil
}

func (api *API) ShowEffect(ctx context.Context, id string) (*workflow.Effect, error) {
	return api.EffectShow(ctx, id)
}

func (api *API) HookList(ctx context.Context) (HookListResult, error) {
	if err := ctx.Err(); err != nil {
		return HookListResult{}, err
	}
	if api.hookCatalog == nil {
		return HookListResult{Hooks: []HookSummary{}}, nil
	}
	ids := make([]string, 0, len(api.hookCatalog.Hooks))
	for id := range api.hookCatalog.Hooks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	hooks := make([]HookSummary, 0, len(ids))
	for _, id := range ids {
		hook := api.hookCatalog.Hooks[id]
		hooks = append(hooks, HookSummary{ID: id, Kind: hook.Kind, Argv: hook.Argv})
	}
	return HookListResult{Hooks: hooks}, nil
}

func (api *API) ListHooks(ctx context.Context) (HookListResult, error) {
	return api.HookList(ctx)
}

func (api *API) HookShow(ctx context.Context, id string) (HookShowResult, error) {
	if err := ctx.Err(); err != nil {
		return HookShowResult{}, err
	}
	if api.hookCatalog == nil {
		return HookShowResult{}, NewValidationError("hook catalog is required", nil)
	}
	hook, ok := api.hookCatalog.Hooks[id]
	if !ok {
		return HookShowResult{}, NewNotFoundError(id, "hook")
	}
	return HookShowResult{ID: id, Hook: hook}, nil
}

func (api *API) ShowHook(ctx context.Context, id string) (HookShowResult, error) {
	return api.HookShow(ctx, id)
}

func (api *API) HookRun(ctx context.Context, params HookRunParams) (*workflow.CheckRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := api.service.RunSingleHook(params.TaskSelector, params.Transition, params.HookID, params.Actor, params.Role, api.hookCatalog, api.templateDir)
	if err != nil {
		return nil, normalizeError(err)
	}
	return run, nil
}

func (api *API) RunHook(ctx context.Context, params HookRunParams) (*workflow.CheckRun, error) {
	return api.HookRun(ctx, params)
}

func (api *API) setObligationStatus(ctx context.Context, params ObligationStatusParams, status string) (*workflow.Obligation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obl, err := api.service.SetObligationStatus(params.TaskSelector, params.ID, status, params.EvidenceID, params.Reason)
	if err != nil {
		return nil, normalizeError(err)
	}
	return obl, nil
}

func templateSummaryFromAny(v any) TemplateSummary {
	switch x := v.(type) {
	case TemplateSummary:
		return x
	case workflow.Template:
		return TemplateSummary{ID: x.ID, Version: x.Version, Kind: x.Kind, Description: x.Description}
	case *workflow.Template:
		if x == nil {
			return TemplateSummary{}
		}
		return TemplateSummary{ID: x.ID, Version: x.Version, Kind: x.Kind, Description: x.Description}
	case map[string]any:
		return TemplateSummary{
			ID:          stringFromAny(x["id"]),
			Version:     stringFromAny(x["version"]),
			Hash:        stringFromAny(x["hash"]),
			Kind:        stringFromAny(x["kind"]),
			Description: stringFromAny(x["description"]),
			InstalledAt: stringFromAny(x["installedAt"]),
			InstalledBy: stringFromAny(x["installedBy"]),
		}
	}
	return TemplateSummary{}
}

func suggestFromAny(out map[string]any) SuggestResult {
	result := SuggestResult{
		Transition: stringFromAny(out["transition"]),
		Warnings:   stringSliceFromAny(out["warnings"]),
	}
	for _, item := range anySlice(out["required"]) {
		req := evidenceRequirementFromAny(item)
		if req.Kind != "" {
			result.Required = append(result.Required, req)
		}
	}
	for _, item := range anySlice(out["missing"]) {
		switch x := item.(type) {
		case string:
			result.Missing = append(result.Missing, x)
		default:
			req := evidenceRequirementFromAny(item)
			if req.Kind != "" {
				result.Missing = append(result.Missing, req.Kind)
			}
		}
	}
	for _, item := range anySlice(out["checks"]) {
		if s := stringFromAny(item); s != "" {
			result.Checks = append(result.Checks, s)
			continue
		}
		if m, ok := item.(map[string]any); ok {
			if id := stringFromAny(m["id"]); id != "" {
				result.Checks = append(result.Checks, id)
			}
		}
	}
	return result
}

func evidenceRequirementFromAny(v any) workflow.EvidenceRequirementSpec {
	switch x := v.(type) {
	case workflow.EvidenceRequirementSpec:
		return x
	case map[string]any:
		return workflow.EvidenceRequirementSpec{
			Kind:  stringFromAny(x["kind"]),
			Facts: rawMessageMapFromAny(x["requiredFacts"]),
		}
	default:
		return workflow.EvidenceRequirementSpec{}
	}
}

func rawMessageMapFromAny(v any) map[string]json.RawMessage {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case map[string]json.RawMessage:
		return x
	case map[string]any:
		out := make(map[string]json.RawMessage, len(x))
		for k, item := range x {
			raw, err := json.Marshal(item)
			if err != nil {
				continue
			}
			out[k] = raw
		}
		return out
	default:
		return nil
	}
}

func anySlice(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []map[string]any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, item)
		}
		return out
	case []string:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, item)
		}
		return out
	case []workflow.EvidenceRequirementSpec:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func stringSliceFromAny(v any) []string {
	var out []string
	for _, item := range anySlice(v) {
		if s := stringFromAny(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func stringFromAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func boolFromAny(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(x)
		return b
	default:
		return false
	}
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr Error
	if errors.As(err, &apiErr) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		return NewNotFoundError("", "")
	}
	var coded codedError
	if errors.As(err, &coded) {
		switch coded.Code() {
		case CodeStaleRevision:
			return NewStaleRevisionError("", 0, 0)
		case CodeContextMismatch:
			return NewContextMismatchError("", "", "")
		case CodeTransitionBlocked:
			return NewTransitionBlockedError("", "", nil)
		case CodeRoleDenied:
			return NewRoleDeniedError("", "", "")
		case CodeIdempotencyMismatch:
			return NewIdempotencyMismatchError("")
		case CodeLeaseConflict:
			return NewLeaseConflictError("", "")
		case CodeEffectNotDeliverable:
			return NewEffectNotDeliverableError("", "")
		}
	}

	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "revision mismatch"):
		return NewStaleRevisionError("", 0, 0)
	case strings.Contains(lower, "context hash mismatch"):
		return NewContextMismatchError("", "", "")
	case strings.Contains(lower, "blocked"):
		return NewTransitionBlockedError("", "", nil)
	case strings.Contains(lower, "not allowed for transition"):
		return NewRoleDeniedError("", "", "")
	case strings.Contains(lower, "not deliverable"):
		return NewEffectNotDeliverableError("", "")
	case strings.Contains(lower, "hook") && (strings.Contains(lower, "failed") || strings.Contains(lower, "exit")):
		return NewHookFailedError("", msg, false)
	case strings.Contains(lower, "not found"):
		kind, ref := splitNotFound(msg)
		return NewNotFoundError(ref, kind)
	case strings.Contains(lower, "invalid") ||
		strings.Contains(lower, " is required") ||
		strings.Contains(lower, "must ") ||
		strings.Contains(lower, "missing ") ||
		strings.Contains(lower, "already installed with different hash") ||
		strings.Contains(lower, "catalog is required"):
		return NewValidationError(msg, nil)
	default:
		return NewInternalError(err)
	}
}

func splitNotFound(msg string) (kind string, ref string) {
	kind = "resource"
	if i := strings.Index(strings.ToLower(msg), " not found"); i > 0 {
		kind = strings.TrimSpace(msg[:i])
	}
	if i := strings.LastIndex(msg, ":"); i >= 0 && i+1 < len(msg) {
		ref = strings.TrimSpace(msg[i+1:])
	}
	return kind, ref
}
