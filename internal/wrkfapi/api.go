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
	"time"

	"github.com/lherron/wrkq/internal/workflow"
)

type API struct {
	service            *workflow.Service
	hookCatalog        *workflow.HookCatalog
	templateDir        string
	hookTimeoutCeiling time.Duration
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

// WithHookTimeoutCeiling bounds external hook execution for remote request
// paths. A zero value preserves the unbounded local/stdio policy.
func WithHookTimeoutCeiling(ceiling time.Duration) Option {
	return func(api *API) {
		api.hookTimeoutCeiling = ceiling
	}
}

func (api *API) WorkflowValidate(ctx context.Context, params WorkflowContentParams) (workflow.ValidateResult, error) {
	if err := ctx.Err(); err != nil {
		return workflow.ValidateResult{}, err
	}
	if err := validateTemplateBody(params.Body, params.SourceName); err != nil {
		return workflow.ValidateResult{}, err
	}
	return api.service.ValidateTemplateContent([]byte(params.Body), api.hookCatalog), nil
}

func (api *API) WorkflowShow(ctx context.Context, ref string) (WorkflowShowResult, error) {
	if err := ctx.Err(); err != nil {
		return WorkflowShowResult{}, err
	}
	info, err := api.service.ShowTemplateVersion(ref)
	if err != nil {
		return WorkflowShowResult{}, normalizeError(err)
	}
	return WorkflowShowResult{
		Template:       *info.Template,
		Hash:           info.Hash,
		DiscontinuedAt: info.DiscontinuedAt,
		DiscontinuedBy: info.DiscontinuedBy,
	}, nil
}

func (api *API) WorkflowDiscontinue(ctx context.Context, ref, actor string) (WorkflowShowResult, error) {
	if err := ctx.Err(); err != nil {
		return WorkflowShowResult{}, err
	}
	id, version, err := workflow.ParseTemplateRef(ref)
	if err != nil {
		return WorkflowShowResult{}, normalizeError(err)
	}
	if err := api.service.DiscontinueTemplate(id, version, actor); err != nil {
		return WorkflowShowResult{}, normalizeError(err)
	}
	return api.WorkflowShow(ctx, ref)
}

func (api *API) WorkflowReinstate(ctx context.Context, ref string) (WorkflowShowResult, error) {
	if err := ctx.Err(); err != nil {
		return WorkflowShowResult{}, err
	}
	id, version, err := workflow.ParseTemplateRef(ref)
	if err != nil {
		return WorkflowShowResult{}, normalizeError(err)
	}
	if err := api.service.ReinstateTemplate(id, version); err != nil {
		return WorkflowShowResult{}, normalizeError(err)
	}
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

func (api *API) WorkflowDiff(ctx context.Context, params WorkflowDiffParams) (DiffResult, error) {
	if err := ctx.Err(); err != nil {
		return DiffResult{}, err
	}
	if err := validateTemplateBody(params.OldBody, params.OldSourceName); err != nil {
		return DiffResult{}, err
	}
	if err := validateTemplateBody(params.NewBody, params.NewSourceName); err != nil {
		return DiffResult{}, err
	}
	if len(params.OldBody)+len(params.NewBody) > MaxTemplateDiffBodyBytes {
		return DiffResult{}, NewValidationError(fmt.Sprintf("template diff bodies exceed %d-byte aggregate limit", MaxTemplateDiffBodyBytes), nil)
	}
	out, err := api.service.DiffTemplateContent([]byte(params.OldBody), []byte(params.NewBody))
	if err != nil {
		return DiffResult{}, normalizeError(err)
	}
	return DiffResult{
		Old:      templateSummaryFromAny(out["old"]),
		New:      templateSummaryFromAny(out["new"]),
		SameHash: boolFromAny(out["sameHash"]),
	}, nil
}

func (api *API) WorkflowInstall(ctx context.Context, params WorkflowInstallParams) (InstallResult, error) {
	if err := ctx.Err(); err != nil {
		return InstallResult{}, err
	}
	if err := validateTemplateBody(params.Body, params.SourceName); err != nil {
		return InstallResult{}, err
	}
	out, err := api.service.InstallTemplateContent([]byte(params.Body), params.PrincipalRef, api.hookCatalog)
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

func validateTemplateBody(body, sourceName string) error {
	if len(body) > MaxTemplateBodyBytes {
		label := "template body"
		if strings.TrimSpace(sourceName) != "" {
			label = sourceName
		}
		return NewValidationError(fmt.Sprintf("%s exceeds %d-byte template body limit", label, MaxTemplateBodyBytes), nil)
	}
	return nil
}

func (api *API) TaskAttach(ctx context.Context, taskSelector, templateRef, actor string, opts ...workflow.AttachTaskOptions) (*workflow.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inst, err := api.service.AttachTask(taskSelector, templateRef, actor, opts...)
	if err != nil {
		return nil, normalizeError(err)
	}
	return inst, nil
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

func (api *API) InstanceShow(ctx context.Context, taskSelector, instanceID string) (*workflow.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inst, err := api.service.ResolveInstance(taskSelector, instanceID)
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

func (api *API) InstanceNext(ctx context.Context, taskSelector, instanceID, role string) (*workflow.NextActionResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inst, err := api.service.ResolveInstance(taskSelector, instanceID)
	if err != nil {
		return nil, normalizeError(err)
	}
	// Resolve onward by the instance's bare task UUID, not inst.TaskRef:
	// task_ref is a project-qualified display ref (e.g. "wrkq:T-00001") that
	// selectors.ResolveTask cannot parse, whereas the UUID always resolves.
	return api.Next(ctx, inst.TaskUUID, role)
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

func (api *API) EvidenceList(ctx context.Context, taskSelector, instanceID string) ([]workflow.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ev, err := api.service.ListEvidence(taskSelector, instanceID)
	if err != nil {
		return nil, normalizeError(err)
	}
	return ev, nil
}

// LedgerAppend records an immutable instance-scoped forensic event. The
// transport resolves and stamps WrittenBy; it is never accepted from callers.
func (api *API) LedgerAppend(ctx context.Context, params LedgerAppendParams) (*LedgerEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, err := api.service.AppendLedger(params)
	if err != nil {
		return nil, normalizeError(err)
	}
	return entry, nil
}

func (api *API) LedgerList(ctx context.Context, params LedgerListParams) (LedgerListResult, error) {
	if err := ctx.Err(); err != nil {
		return LedgerListResult{}, err
	}
	entries, err := api.service.ListLedger(params)
	if err != nil {
		return LedgerListResult{}, normalizeError(err)
	}
	return entries, nil
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

func (api *API) ObligationSatisfy(ctx context.Context, params ObligationStatusParams) (*workflow.Obligation, error) {
	return api.setObligationStatus(ctx, params, "satisfied")
}

func (api *API) ObligationWaive(ctx context.Context, params ObligationStatusParams) (*workflow.Obligation, error) {
	return api.setObligationStatus(ctx, params, "waived")
}

func (api *API) ObligationCancel(ctx context.Context, params ObligationStatusParams) (*workflow.Obligation, error) {
	return api.setObligationStatus(ctx, params, "cancelled")
}

func (api *API) CheckPreflight(ctx context.Context, taskSelector, transition, role string) (*workflow.NextActionResponse, error) {
	return api.Next(ctx, taskSelector, role)
}

func (api *API) CheckRun(ctx context.Context, params CheckRunParams) (CheckRunResult, error) {
	if err := ctx.Err(); err != nil {
		return CheckRunResult{}, err
	}
	runs, err := api.service.RunChecksWithOptions(params.TaskSelector, params.Transition, params.PrincipalRef, params.Role, api.hookCatalog, api.templateDir, workflow.HookExecutionOptions{
		Context: ctx, TimeoutCeiling: api.hookTimeoutCeiling,
	})
	if err != nil {
		return CheckRunResult{}, normalizeError(err)
	}
	return CheckRunResult{Runs: runs}, nil
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

func (api *API) HookRun(ctx context.Context, params HookRunParams) (*workflow.CheckRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := api.service.RunSingleHookWithOptions(params.TaskSelector, params.Transition, params.HookID, params.PrincipalRef, params.Role, api.hookCatalog, api.templateDir, workflow.HookExecutionOptions{
		Context: ctx, TimeoutCeiling: api.hookTimeoutCeiling,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	return run, nil
}

func (api *API) setObligationStatus(ctx context.Context, params ObligationStatusParams, status string) (*workflow.Obligation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obl, err := api.service.SetObligationStatusWithAuthority(params.TaskSelector, params.ID, status, params.EvidenceID, params.Reason, workflow.ObligationStatusOptions{PrincipalRef: params.PrincipalRef, Role: params.Role})
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
			ID:             stringFromAny(x["id"]),
			Version:        stringFromAny(x["version"]),
			Hash:           stringFromAny(x["hash"]),
			Kind:           stringFromAny(x["kind"]),
			Description:    stringFromAny(x["description"]),
			InstalledAt:    stringFromAny(x["installedAt"]),
			InstalledBy:    stringFromAny(x["installedBy"]),
			DiscontinuedAt: stringFromAny(x["discontinuedAt"]),
			DiscontinuedBy: stringFromAny(x["discontinuedBy"]),
		}
	case map[string]string:
		return TemplateSummary{
			ID:             x["id"],
			Version:        x["version"],
			Hash:           x["hash"],
			Kind:           x["kind"],
			Description:    x["description"],
			InstalledAt:    x["installedAt"],
			InstalledBy:    x["installedBy"],
			DiscontinuedAt: x["discontinuedAt"],
			DiscontinuedBy: x["discontinuedBy"],
		}
	}
	return TemplateSummary{}
}

func suggestFromAny(out map[string]any) SuggestResult {
	result := SuggestResult{
		Transition: stringFromAny(out["transition"]),
		Required:   []workflow.EvidenceRequirementSpec{},
		Missing:    []string{},
		Checks:     []string{},
		Warnings:   stringSliceFromAny(out["warnings"]),
	}
	if result.Warnings == nil {
		result.Warnings = []string{}
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
		case CodeTransitionBlocked:
			return NewTransitionBlockedError("", "", nil)
		case CodeRoleDenied:
			return NewRoleDeniedError("", "", "")
		case CodeIdempotencyMismatch:
			return NewIdempotencyMismatchError("")
		case CodeLeaseConflict:
			if detail, ok := workflow.AsErrorDetail(err); ok {
				return NewDetailError(detail, true)
			}
			if strings.Contains(err.Error(), "action lease conflict") {
				return newError(CodeLeaseConflict, err.Error(), true, nil, err)
			}
			return NewLeaseConflictError("", "")
		case CodeEffectNotDeliverable:
			return NewEffectNotDeliverableError("", "")
		case CodeEffectDeliveryFailed:
			if detail, ok := workflow.AsErrorDetail(err); ok {
				return NewDetailError(detail, true)
			}
			return newError(CodeEffectDeliveryFailed, err.Error(), true, nil, err)
		case CodeSuspended:
			if detail, ok := workflow.AsErrorDetail(err); ok {
				return NewDetailError(detail, false)
			}
			return newError(CodeSuspended, err.Error(), false, nil, err)
		case CodeSuspensionNotFound:
			if detail, ok := workflow.AsErrorDetail(err); ok {
				return NewDetailError(detail, false)
			}
			return newError(CodeSuspensionNotFound, err.Error(), false, nil, err)
		case CodeValidation, CodeKindRoleDenied, CodeLinkageUnresolved, CodeLinkageStale:
			if detail, ok := workflow.AsErrorDetail(err); ok {
				return NewDetailError(detail, false)
			}
			return NewValidationError(err.Error(), nil)
		}
	}

	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "revision mismatch"):
		return NewStaleRevisionError("", 0, 0)
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
		strings.Contains(lower, "catalog is required") ||
		strings.Contains(lower, "hook catalog hash mismatch") ||
		strings.Contains(lower, "daemon hook catalog is not configured") ||
		strings.Contains(lower, "has no pinned hook catalog"):
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
