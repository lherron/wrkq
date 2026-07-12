package wrkfrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/wrkfapi"
)

const ProtocolVersion = "2026-06-30"

type RegistryOptions struct {
	DatabasePath  string
	SchemaHash    string
	ServerVersion string
	DefaultActor  string
	DefaultRole   string
}

func RegisterAPI(s *Server, api *wrkfapi.API, opts RegistryOptions) {
	if opts.ServerVersion == "" {
		opts.ServerVersion = "dev"
	}
	s.Register("wrkf.initialize", HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.ProtocolVersion != ProtocolVersion {
			return nil, wrkfapi.NewValidationError("invalid protocolVersion", map[string]any{
				"expected": ProtocolVersion,
				"actual":   params.ProtocolVersion,
			})
		}
		return marshalResult(map[string]any{
			"protocolVersion": ProtocolVersion,
			"server": map[string]any{
				"name":    "wrkf",
				"version": opts.ServerVersion,
				"pid":     os.Getpid(),
			},
			"database": map[string]any{
				"path": opts.DatabasePath,
			},
			"capabilities": map[string]any{
				"cancel":             true,
				"actionNext":         true,
				"actionClaim":        true,
				"actionSettle":       true,
				"effectClaimLease":   true,
				"runExternalBinding": true,
			},
			"schemaHash": opts.SchemaHash,
		})
	}))

	s.Register("wrkf.workflow.validate", apiHandler(func(ctx context.Context, p workflowPathParams) (any, error) {
		return api.WorkflowValidate(ctx, p.Path)
	}))
	s.Register("wrkf.workflow.show", apiHandler(func(ctx context.Context, p refParams) (any, error) {
		return api.WorkflowShow(ctx, p.Ref)
	}))
	s.Register("wrkf.workflow.list", apiHandler(func(ctx context.Context, _ emptyParams) (any, error) {
		return api.WorkflowList(ctx)
	}))
	s.Register("wrkf.workflow.diff", apiHandler(func(ctx context.Context, p diffParams) (any, error) {
		return api.WorkflowDiff(ctx, p.OldPath, p.NewPath)
	}))
	s.Register("wrkf.workflow.install", apiHandler(func(ctx context.Context, p installParams) (any, error) {
		return api.WorkflowInstall(ctx, p.Path, defaultString(p.PrincipalRef, opts.DefaultActor))
	}))
	s.Register("wrkf.task.attach", apiHandler(func(ctx context.Context, p taskAttachParams) (any, error) {
		return api.TaskAttach(ctx, p.TaskSelector, p.Workflow, defaultString(p.PrincipalRef, opts.DefaultActor), workflow.AttachTaskOptions{
			Supersede:             p.Supersede,
			PredecessorInstanceID: p.PredecessorInstanceID,
			PredecessorRevision:   p.PredecessorRevision,
		})
	}))
	s.Register("wrkf.task.inspect", apiHandler(func(ctx context.Context, p taskParams) (any, error) {
		return api.TaskInspect(ctx, p.TaskSelector)
	}))
	s.Register("wrkf.task.timeline", apiHandler(func(ctx context.Context, p taskParams) (any, error) {
		return api.TaskTimeline(ctx, p.TaskSelector)
	}))
	s.Register("wrkf.task.refresh", apiHandler(func(ctx context.Context, p taskActorParams) (any, error) {
		return api.TaskRefresh(ctx, p.TaskSelector, defaultString(p.PrincipalRef, opts.DefaultActor))
	}))
	s.Register("wrkf.next", apiHandler(func(ctx context.Context, p nextParams) (any, error) {
		return api.Next(ctx, p.TaskSelector, defaultString(p.Role, opts.DefaultRole))
	}))
	s.Register("wrkf.evidence.add", apiHandler(func(ctx context.Context, p wrkfapi.EvidenceAddParams) (any, error) {
		p.PrincipalRef = defaultString(p.PrincipalRef, opts.DefaultActor)
		p.Role = defaultString(p.Role, opts.DefaultRole)
		return api.EvidenceAdd(ctx, p)
	}))
	s.Register("wrkf.evidence.list", apiHandler(func(ctx context.Context, p taskParams) (any, error) {
		return api.EvidenceList(ctx, p.TaskSelector)
	}))
	s.Register("wrkf.evidence.show", apiHandler(func(ctx context.Context, p idParams) (any, error) {
		return api.EvidenceShow(ctx, p.ID)
	}))
	s.Register("wrkf.evidence.suggest", apiHandler(func(ctx context.Context, p taskTransitionParams) (any, error) {
		return api.EvidenceSuggest(ctx, p.TaskSelector, p.Transition)
	}))
	s.Register("wrkf.obligation.list", apiHandler(func(ctx context.Context, p obligationListParams) (any, error) {
		return api.ObligationList(ctx, p.TaskSelector, p.IncludeClosed || p.All)
	}))
	s.Register("wrkf.obligation.show", apiHandler(func(ctx context.Context, p idParams) (any, error) {
		return api.ObligationShow(ctx, p.ID)
	}))
	s.Register("wrkf.obligation.satisfy", obligationStatusHandler(api.ObligationSatisfy))
	s.Register("wrkf.obligation.waive", obligationStatusHandler(api.ObligationWaive))
	s.Register("wrkf.obligation.cancel", obligationStatusHandler(api.ObligationCancel))
	s.Register("wrkf.check.preflight", apiHandler(func(ctx context.Context, p checkParams) (any, error) {
		return api.CheckPreflight(ctx, p.TaskSelector, p.Transition, defaultString(p.Role, opts.DefaultRole))
	}))
	s.Register("wrkf.check.run", apiHandler(func(ctx context.Context, p wrkfapi.CheckRunParams) (any, error) {
		p.PrincipalRef = defaultString(p.PrincipalRef, opts.DefaultActor)
		p.Role = defaultString(p.Role, opts.DefaultRole)
		return api.CheckRun(ctx, p)
	}))
	s.Register("wrkf.check.show", apiHandler(func(ctx context.Context, p idParams) (any, error) {
		return api.CheckShow(ctx, p.ID)
	}))
	s.Register("wrkf.check.list", apiHandler(func(ctx context.Context, p checkListParams) (any, error) {
		return api.CheckList(ctx, p.TaskSelector, p.Transition)
	}))
	s.Register("wrkf.hook.list", apiHandler(func(ctx context.Context, _ emptyParams) (any, error) {
		return api.HookList(ctx)
	}))
	s.Register("wrkf.hook.show", apiHandler(func(ctx context.Context, p idParams) (any, error) {
		return api.HookShow(ctx, p.ID)
	}))
	s.Register("wrkf.hook.run", apiHandler(func(ctx context.Context, p wrkfapi.HookRunParams) (any, error) {
		p.PrincipalRef = defaultString(p.PrincipalRef, opts.DefaultActor)
		p.Role = defaultString(p.Role, opts.DefaultRole)
		return api.HookRun(ctx, p)
	}))
	s.Register("wrkf.transition.apply", apiHandler(func(ctx context.Context, p wrkfapi.TransitionApplyParams) (any, error) {
		p.PrincipalRef = defaultString(p.PrincipalRef, opts.DefaultActor)
		p.Role = defaultString(p.Role, opts.DefaultRole)
		return api.TransitionApply(ctx, p)
	}))
	s.Register("wrkf.run.start", apiHandler(func(ctx context.Context, p wrkfapi.RunStartParams) (any, error) {
		p.PrincipalRef = defaultString(p.PrincipalRef, opts.DefaultActor)
		p.Role = defaultString(p.Role, opts.DefaultRole)
		return api.RunStart(ctx, p)
	}))
	s.Register("wrkf.run.bindExternal", apiHandler(func(ctx context.Context, p wrkfapi.RunBindExternalParams) (any, error) {
		return api.RunBindExternal(ctx, p)
	}))
	s.Register("wrkf.run.finish", apiHandler(func(ctx context.Context, p wrkfapi.RunFinishParams) (any, error) {
		return api.RunFinish(ctx, p)
	}))
	s.Register("wrkf.run.fail", apiHandler(func(ctx context.Context, p wrkfapi.RunFailParams) (any, error) {
		return api.RunFail(ctx, p)
	}))
	s.Register("wrkf.run.show", apiHandler(func(ctx context.Context, p idParams) (any, error) {
		return api.RunShow(ctx, p.ID)
	}))
	s.Register("wrkf.run.list", apiHandler(func(ctx context.Context, p taskParams) (any, error) {
		return api.RunList(ctx, p.TaskSelector)
	}))
	s.Register("wrkf.action.next", apiHandler(func(ctx context.Context, p wrkfapi.ActionNextParams) (any, error) {
		return api.ActionNext(ctx, p)
	}))
	s.Register("wrkf.action.claim", apiHandler(func(ctx context.Context, p wrkfapi.ActionClaimParams) (any, error) {
		return api.ActionClaim(ctx, p)
	}))
	s.Register("wrkf.action.settle", apiHandler(func(ctx context.Context, p wrkfapi.ActionSettleParams) (any, error) {
		return api.ActionSettle(ctx, p)
	}))
	s.Register("wrkf.action.start", apiHandler(func(ctx context.Context, p wrkfapi.ActionStartParams) (any, error) {
		p.PrincipalRef = defaultString(p.PrincipalRef, opts.DefaultActor)
		return api.ActionStart(ctx, p)
	}))
	s.Register("wrkf.action.bindExternal", apiHandler(func(ctx context.Context, p wrkfapi.ActionBindExternalParams) (any, error) {
		return api.ActionBindExternal(ctx, p)
	}))
	s.Register("wrkf.action.complete", apiHandler(func(ctx context.Context, p wrkfapi.ActionCompleteParams) (any, error) {
		return api.ActionComplete(ctx, p)
	}))
	s.Register("wrkf.action.fail", apiHandler(func(ctx context.Context, p wrkfapi.ActionFailParams) (any, error) {
		return api.ActionFail(ctx, p)
	}))
	s.Register("wrkf.action.heartbeat", apiHandler(func(ctx context.Context, p wrkfapi.ActionHeartbeatParams) (any, error) {
		return api.ActionHeartbeat(ctx, p)
	}))
	s.Register("wrkf.action.renewLease", apiHandler(func(ctx context.Context, p wrkfapi.ActionHeartbeatParams) (any, error) {
		return api.ActionHeartbeat(ctx, p)
	}))
	s.Register("wrkf.action.show", apiHandler(func(ctx context.Context, p wrkfapi.ActionShowParams) (any, error) {
		return api.ActionShow(ctx, p)
	}))
	s.Register("wrkf.action.list", apiHandler(func(ctx context.Context, p wrkfapi.ActionListParams) (any, error) {
		return api.ActionList(ctx, p)
	}))
	s.Register("wrkf.effect.list", apiHandler(func(ctx context.Context, p effectListParams) (any, error) {
		return api.EffectList(ctx, p.TaskSelector, p.All)
	}))
	s.Register("wrkf.effect.show", apiHandler(func(ctx context.Context, p idParams) (any, error) {
		return api.EffectShow(ctx, p.ID)
	}))
	s.Register("wrkf.effect.claim", apiHandler(func(ctx context.Context, p wrkfapi.EffectClaimParams) (any, error) {
		p.Adapter = defaultString(p.Adapter, opts.DefaultActor)
		return api.EffectClaim(ctx, p)
	}))
	s.Register("wrkf.effect.ack", apiHandler(func(ctx context.Context, p wrkfapi.EffectAckParams) (any, error) {
		return api.EffectAck(ctx, p)
	}))
	s.Register("wrkf.effect.fail", apiHandler(func(ctx context.Context, p wrkfapi.EffectFailParams) (any, error) {
		return api.EffectFail(ctx, p)
	}))
	s.Register("wrkf.effect.retry", apiHandler(func(ctx context.Context, p effectRetryParams) (any, error) {
		id := p.EffectID
		if id == "" {
			id = p.ID
		}
		return api.EffectRetry(ctx, id)
	}))
	s.Register("wrkf.effect.deliver", apiHandler(func(ctx context.Context, p wrkfapi.EffectDeliverParams) (any, error) {
		p.Adapter = defaultString(p.Adapter, opts.DefaultActor)
		return api.EffectDeliver(ctx, p)
	}))
}

func SchemaHash(database *db.DB) string {
	if database == nil {
		return "sha256:" + strings.Repeat("0", 64)
	}
	applied, pending, err := database.MigrationStatus()
	if err != nil {
		return "sha256:" + strings.Repeat("0", 64)
	}
	h := sha256.New()
	for _, migration := range applied {
		_, _ = fmt.Fprintln(h, "applied:"+migration)
	}
	for _, migration := range pending {
		_, _ = fmt.Fprintln(h, "pending:"+migration)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

type rpcFunc[P any] func(context.Context, P) (any, error)

func apiHandler[P any](fn rpcFunc[P]) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var params P
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := fn(ctx, params)
		if err != nil {
			return nil, err
		}
		return marshalResult(result)
	}
}

func obligationStatusHandler(fn func(context.Context, wrkfapi.ObligationStatusParams) (*wrkfapi.Obligation, error)) HandlerFunc {
	return apiHandler(func(ctx context.Context, p wrkfapi.ObligationStatusParams) (any, error) {
		return fn(ctx, p)
	})
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	if err := rejectLegacyActorFields(raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return wrkfapi.NewValidationError("invalid params", nil)
	}
	return nil
}

// legacyActorParamFields are the actor-shaped wrkf participant-identity param
// keys retired by the T-05372 cutover. They are a deliberate protocol break:
// wrkf participant identity is now canonical `principal_ref`. A request that
// still carries any of these top-level keys is rejected rather than silently
// ignored by json.Unmarshal. Scan is top-level only so free-form nested blobs
// (evidence data/facts/source) that legitimately contain an "actor" key are
// not affected.
var legacyActorParamFields = []string{
	"actor", "actorRole", "actor_role",
	"ownerActor", "owner_actor",
	"obligeeActor", "obligee_actor",
	"waiveActor", "waive_actor",
	"resolvedByActor", "resolved_by_actor",
}

func rejectLegacyActorFields(raw json.RawMessage) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed[0] != '{' {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil // malformed object is handled by the real decode below
	}
	for _, key := range legacyActorParamFields {
		if _, ok := top[key]; ok {
			return wrkfapi.NewValidationError("legacy actor-shaped wrkf param rejected", map[string]any{
				"field":    key,
				"expected": "principal_ref",
				"hint":     "wrkf participant identity is canonical principal_ref (agent:<id>); the actor field was removed in the T-05372 protocol break",
			})
		}
	}
	return nil
}

func marshalResult(v any) (json.RawMessage, error) {
	if raw, ok := v.(json.RawMessage); ok {
		return raw, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

type emptyParams struct{}

type workflowPathParams struct {
	Path string `json:"path"`
}

type refParams struct {
	Ref string `json:"ref"`
}

type diffParams struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
}

type installParams struct {
	Path         string `json:"path"`
	PrincipalRef string `json:"principal_ref,omitempty"`
}

type taskParams struct {
	TaskSelector string `json:"task"`
}

type taskActorParams struct {
	TaskSelector string `json:"task"`
	PrincipalRef string `json:"principal_ref,omitempty"`
}

type taskAttachParams struct {
	TaskSelector          string `json:"task"`
	Workflow              string `json:"workflow"`
	Supersede             bool   `json:"supersede,omitempty"`
	PredecessorInstanceID string `json:"predecessorInstanceId,omitempty"`
	PredecessorRevision   *int64 `json:"predecessorRevision,omitempty"`
	PrincipalRef          string `json:"principal_ref,omitempty"`
}

type nextParams struct {
	TaskSelector string `json:"task"`
	Role         string `json:"role,omitempty"`
}

type taskTransitionParams struct {
	TaskSelector string `json:"task"`
	Transition   string `json:"transition"`
}

type obligationListParams struct {
	TaskSelector  string `json:"task"`
	IncludeClosed bool   `json:"includeClosed,omitempty"`
	All           bool   `json:"all,omitempty"`
}

type checkParams struct {
	TaskSelector string `json:"task"`
	Transition   string `json:"transition"`
	Role         string `json:"role,omitempty"`
}

type checkListParams struct {
	TaskSelector string `json:"task"`
	Transition   string `json:"transition,omitempty"`
}

type effectListParams struct {
	TaskSelector string `json:"task"`
	All          bool   `json:"all,omitempty"`
}

type effectRetryParams struct {
	EffectID string `json:"effectId"`
	ID       string `json:"id"`
}

type idParams struct {
	ID string `json:"id"`
}
