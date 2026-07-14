package workrpc

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/lherron/wrkq/internal/wrkqapi"
)

type RegistryOptions struct {
	Database         *db.DB
	DatabasePath     string
	MigrationHash    string
	ServerVersion    string
	Entrypoint       string
	DefaultActor     string
	WrkqDefaultActor string
	UseWrkqDefault   bool
	DefaultRole      string
	AttachDir        string
	AttachmentsMaxMB int

	// Search carries the SERVER-owned search/index host configuration (T-05114).
	// The server owns the derived <db>.search.sqlite sidecar + dense embedder
	// behind wrkq.search.listView / wrkq.index.*; the mirror NEVER opens the
	// sidecar or calls EnsureLlamaReady. Zero-value (Enabled=false) disables the
	// search host (search/index methods report WRKQ_VALIDATION "search is
	// disabled"). bootstrap.Server populates this from config.SearchConfig.
	Search wrkqapi.SearchConfig
}

func RegisterAPI(s *Server, api *wrkfapi.API, opts RegistryOptions) {
	if opts.DatabasePath == "" && opts.Database != nil {
		opts.DatabasePath = opts.Database.Path()
	}
	if opts.MigrationHash == "" {
		opts.MigrationHash = MigrationHash(opts.Database)
	}

	s.Register("rpc.initialize", HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.ProtocolVersion != ProtocolVersion {
			return nil, NewDomainError(wrkfapi.CodeValidation, "invalid protocolVersion", false, map[string]any{
				"expected": ProtocolVersion,
				"actual":   params.ProtocolVersion,
			})
		}
		return marshalResult(newInitializeResult(opts, s.RegisteredMethods()))
	}))
	s.Register("rpc.shutdown", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}))
	s.Register("rpc.exit", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`null`), nil
	}))
	s.Register("$/cancelRequest", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`null`), nil
	}))

	registerWrkqMethods(s, api, opts)
	registerWrkfMethods(s, api, opts)
}

func NewRegistry(api *wrkfapi.API, opts RegistryOptions) *Server {
	srv := NewServer(ioDiscard{})
	RegisterAPI(srv, api, opts)
	return srv
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func MethodCatalog() []string {
	methods := append([]string{}, methodCatalog...)
	sort.Strings(methods)
	return methods
}

func ErrorCodeCatalog() []string {
	codes := []string{
		CodeWRKQNotFound,
		CodeWRKQValidation,
		CodeWRKQConflict,
		CodeWRKQPermissionDenied,
		CodeWRKQMigrationRequired,
		wrkfapi.CodeNotFound,
		wrkfapi.CodeValidation,
		wrkfapi.CodeStaleRevision,
		wrkfapi.CodeTransitionBlocked,
		wrkfapi.CodeRoleDenied,
		wrkfapi.CodeIdempotencyMismatch,
		wrkfapi.CodeLeaseConflict,
		wrkfapi.CodeEffectNotDeliverable,
		wrkfapi.CodeHookFailed,
		wrkfapi.CodeKindRoleDenied,
		wrkfapi.CodeLinkageUnresolved,
		wrkfapi.CodeLinkageStale,
		wrkfapi.CodeSuspended,
		wrkfapi.CodeSuspensionNotFound,
		CodeWorkRPCInternal,
	}
	sort.Strings(codes)
	return codes
}

var methodCatalog = []string{
	"rpc.initialize",
	"rpc.shutdown",
	"rpc.exit",
	"$/cancelRequest",
	"wrkq.task.create",
	"wrkq.task.show",
	"wrkq.task.catView",
	"wrkq.task.list",
	"wrkq.task.lsView",
	"wrkq.task.findListView",
	"wrkq.history.listView",
	"wrkq.history.tailView",
	"wrkq.monitor.eventsView",
	"wrkq.monitor.stateView",
	"wrkq.task.treeView",
	"wrkq.task.blockedView",
	"wrkq.task.inboxView",
	"wrkq.task.copy",
	"wrkq.task.update",
	"wrkq.task.move",
	"wrkq.task.acknowledge",
	"wrkq.task.delete",
	"wrkq.task.restore",
	"wrkq.comment.add",
	"wrkq.comment.list",
	"wrkq.comment.show",
	"wrkq.comment.catView",
	"wrkq.comment.listView",
	"wrkq.comment.delete",
	"wrkq.attachment.add",
	"wrkq.attachment.addBytes",
	"wrkq.attachment.getBytes",
	"wrkq.attachment.list",
	"wrkq.attachment.listView",
	"wrkq.attachment.show",
	"wrkq.attachment.remove",
	"wrkq.relation.add",
	"wrkq.relation.list",
	"wrkq.relation.listView",
	"wrkq.relation.remove",
	"wrkq.container.create",
	"wrkq.container.update",
	"wrkq.container.move",
	"wrkq.container.webhookSet",
	"wrkq.container.archive",
	"wrkq.container.restore",
	"wrkq.container.delete",
	"wrkq.container.deleteRecursive",
	"wrkq.container.show",
	"wrkq.container.catView",
	"wrkq.container.list",
	"wrkq.project.listView",
	"wrkq.project.setRoot",
	"wrkq.webhook.add",
	"wrkq.webhook.remove",
	"wrkq.webhook.listView",
	"wrkq.workflow.attach",
	"wrkq.workflow.inspect",
	"wrkq.workflow.timeline",
	"wrkq.workflow.refresh",
	"wrkq.handoff.create",
	"wrkq.handoff.get",
	"wrkq.handoff.listView",
	"wrkq.handoff.searchView",
	"wrkq.handoff.acknowledge",
	"wrkq.search.listView",
	"wrkq.index.status",
	"wrkq.index.update",
	"wrkq.index.rebuild",
	"wrkq.index.vacuum",
	"wrkq.index.pause",
	"wrkq.index.resume",
	"wrkf.workflow.validate",
	"wrkf.workflow.show",
	"wrkf.workflow.list",
	"wrkf.workflow.diff",
	"wrkf.workflow.install",
	"wrkf.workflow.discontinue",
	"wrkf.workflow.reinstate",
	"wrkf.instance.show",
	"wrkf.instance.next",
	"wrkf.evidence.add",
	"wrkf.evidence.list",
	"wrkf.evidence.show",
	"wrkf.evidence.suggest",
	"wrkf.ledger.append",
	"wrkf.ledger.list",
	"wrkf.event.query",
	"wrkf.role.list",
	"wrkf.role.bind",
	"wrkf.role.unbind",
	"wrkf.role.set",
	"wrkf.obligation.list",
	"wrkf.obligation.show",
	"wrkf.obligation.satisfy",
	"wrkf.obligation.waive",
	"wrkf.obligation.cancel",
	"wrkf.check.preflight",
	"wrkf.check.run",
	"wrkf.check.show",
	"wrkf.check.list",
	"wrkf.hook.list",
	"wrkf.hook.show",
	"wrkf.hook.run",
	"wrkf.transition.apply",
	"wrkf.suspension.resolve",
	"wrkf.run.start",
	"wrkf.run.bindExternal",
	"wrkf.run.finish",
	"wrkf.run.fail",
	"wrkf.run.show",
	"wrkf.run.list",
	"wrkf.action.next",
	"wrkf.action.claim",
	"wrkf.action.settle",
	"wrkf.action.start",
	"wrkf.action.bindExternal",
	"wrkf.action.complete",
	"wrkf.action.fail",
	"wrkf.action.heartbeat",
	"wrkf.action.renewLease",
	"wrkf.action.show",
	"wrkf.action.list",
	"wrkf.effect.list",
	"wrkf.effect.show",
	"wrkf.effect.claim",
	"wrkf.effect.ack",
	"wrkf.effect.fail",
	"wrkf.effect.retry",
	"wrkf.effect.deliver",
}

var dtoCatalog = []string{
	"WrkqTask",
	"WrkqTaskCopyParams",     // wrkq.task.copy request envelope (server-owned deep copy)
	"WrkqTaskCopyResult",     // wrkq.task.copy per-source outcome (legacy copyResult output shape)
	"WrkqTaskCatView",        // CLI compatibility projection (cat --json); nested CatView* structs are part of this DTO
	"WrkqContainerCatView",   // CLI compatibility projection (container cat)
	"WrkqCommentCatView",     // CLI compatibility projection (comment cat)
	"WrkqCommentListView",    // CLI compatibility list projection (comment ls)
	"WrkqAttachmentListView", // CLI compatibility list projection (attach ls)
	"WrkqLsListView",         // CLI compatibility list projection (ls)
	"WrkqFindListView",       // CLI compatibility list projection (find)
	"WrkqHistoryListView",    // CLI compatibility history read model (log over event_log); nested WrkqLogEvent is part of this DTO
	"WrkqLogEvent",           // element of history.listView (the legacy logEvent row shape)
	"WrkqHistoryTailView",    // CLI compatibility bounded ASCENDING raw tail (watch / monitor --raw); nested WrkqWatchEvent is part of this DTO
	"WrkqWatchEvent",         // element of history.tailView (the legacy watchEvent row shape — INCLUDES resource_id, distinct from WrkqLogEvent)
	"WrkqMonitorEventsView",  // CLI compatibility bounded ASCENDING filtered event page (monitor watch); nested WrkqMonitorEvent is part of this DTO
	"WrkqMonitorEvent",       // element of monitor.eventsView (the legacy monitorEventLine data row)
	"WrkqMonitorStateView",   // CLI compatibility single --until condition snapshot (monitor watch/wait)
	"WrkqTreeView",           // CLI compatibility tree projection (tree); nested WrkqTreeNode is part of this DTO
	"WrkqTaskBlockedView",    // CLI compatibility projection (check blocked); nested WrkqTaskBlockedEntry is part of this DTO
	"WrkqInboxView",          // CLI compatibility list projection (check-inbox); nested WrkqInboxEntry is part of this DTO
	"CatViewRelation",        // element of relation.listView (also nested in WrkqTaskCatView)
	"WrkqTaskListResult",
	"WrkqComment",
	"WrkqCommentListResult",
	"WrkqAttachment",
	"WrkqAttachmentBytes",          // byte-transfer read chunk (attach get); byte-transfer boundary, not canonical
	"WrkqAttachmentAddBytesResult", // byte-transfer upload ack (attach put -); byte-transfer boundary, not canonical
	"WrkqRelation",
	"WrkqContainer",
	"WrkqContainerListResult",
	"WrkqProjectsListView", // CLI compatibility projection (projects)
	"WrkqProjectEntry",     // element of projects listView (includes nullable registered root)
	"WebhookRow",           // element of wrkq.webhook.listView (legacy {url:<value>} row)
	"WrkqWorkflowAttachResult",
	"WrkqWorkflowInspectResult",
	"WrkqHandoff",             // handoff resource DTO (legacy handoffJSON field order)
	"WrkqHandoffCreateResult", // wrkq.handoff.create envelope (handoff + idempotentReplay)
	"WrkqHandoffListResult",   // wrkq.handoff.listView page envelope
	"WrkqHandoffSearchResult", // wrkq.handoff.searchView legacy search output envelope
	"WrkqSearchListView",      // CLI compatibility search read model (search); nested WrkqSearchResult + WrkqIndexStatus are part of this DTO
	"WrkqSearchResult",        // element of search.listView (legacy search.Result struct order)
	"WrkqIndexStatus",         // CLI compatibility index status projection (index status; legacy indexdb.Status struct order)
	"WrkfInstance",
	"WrkfEvent",
	"WrkfEventQueryResult",
	"WrkfQueriedEvent",
	"WrkfEvidence",
	"WrkfLedgerEntry",
	"WrkfLedgerListResult",
	"WrkfObligation",
	"WrkfEffect",
	"WrkfRun",
	"WrkfCheckRun",
	"WrkfTransitionResult",
	"WrkfSuspensionResolveResult", // wrkf.suspension.resolve output (cleared suspension + new state/effects)
	"WrkfWorkflowTemplateSummary",
	"WrkfWorkflowListResult",
	"WrkfWorkflowShowResult",
	"WrkfInstallResult",
	"WrkfDiffResult",
	"WrkfSuggestResult",
	"WrkfEffectClaimResult",
}

func registerWrkqMethods(s *Server, api *wrkfapi.API, opts RegistryOptions) {
	wrkqDefaultActor := opts.DefaultActor
	if opts.UseWrkqDefault {
		wrkqDefaultActor = opts.WrkqDefaultActor
	}
	wq := wrkqapi.New(opts.Database, api, wrkqDefaultActor, opts.AttachDir, opts.AttachmentsMaxMB, wrkqapi.WithSearch(opts.Search))

	s.Register("wrkq.task.create", apiHandler(func(ctx context.Context, p wrkqapi.TaskCreateParams) (any, error) {
		return wq.TaskCreate(ctx, p)
	}))
	s.Register("wrkq.task.show", apiHandler(func(ctx context.Context, p wrkqapi.TaskShowParams) (any, error) {
		return wq.TaskShow(ctx, p)
	}))
	s.Register("wrkq.task.catView", apiHandler(func(ctx context.Context, p wrkqapi.TaskCatViewParams) (any, error) {
		return wq.TaskCatView(ctx, p)
	}))
	s.Register("wrkq.task.list", apiHandler(func(ctx context.Context, p wrkqapi.TaskListParams) (any, error) {
		return wq.TaskList(ctx, p)
	}))
	s.Register("wrkq.task.lsView", apiHandler(func(ctx context.Context, p wrkqapi.LsListViewParams) (any, error) {
		return wq.LsListView(ctx, p)
	}))
	s.Register("wrkq.task.findListView", apiHandler(func(ctx context.Context, p wrkqapi.FindListViewParams) (any, error) {
		return wq.FindListView(ctx, p)
	}))
	s.Register("wrkq.history.listView", historyListViewHandler(wq))
	s.Register("wrkq.history.tailView", apiHandler(func(ctx context.Context, p wrkqapi.HistoryTailViewParams) (any, error) {
		return wq.HistoryTailView(ctx, p)
	}))
	s.Register("wrkq.monitor.eventsView", apiHandler(func(ctx context.Context, p wrkqapi.MonitorEventsViewParams) (any, error) {
		return wq.MonitorEventsView(ctx, p)
	}))
	s.Register("wrkq.monitor.stateView", apiHandler(func(ctx context.Context, p wrkqapi.MonitorStateViewParams) (any, error) {
		return wq.MonitorStateView(ctx, p)
	}))
	s.Register("wrkq.task.treeView", apiHandler(func(ctx context.Context, p wrkqapi.TreeViewParams) (any, error) {
		return wq.TreeView(ctx, p)
	}))
	s.Register("wrkq.task.blockedView", apiHandler(func(ctx context.Context, p wrkqapi.TaskBlockedViewParams) (any, error) {
		return wq.TaskBlockedView(ctx, p)
	}))
	s.Register("wrkq.task.inboxView", apiHandler(func(ctx context.Context, p wrkqapi.InboxViewParams) (any, error) {
		return wq.InboxView(ctx, p)
	}))
	s.Register("wrkq.task.copy", apiHandler(func(ctx context.Context, p wrkqapi.TaskCopyParams) (any, error) {
		return wq.TaskCopy(ctx, p)
	}))
	s.Register("wrkq.task.update", apiHandler(func(ctx context.Context, p wrkqapi.TaskUpdateParams) (any, error) {
		return wq.TaskUpdate(ctx, p)
	}))
	s.Register("wrkq.task.move", apiHandler(func(ctx context.Context, p wrkqapi.TaskMoveParams) (any, error) {
		return wq.TaskMove(ctx, p)
	}))
	s.Register("wrkq.task.acknowledge", apiHandler(func(ctx context.Context, p wrkqapi.TaskAcknowledgeParams) (any, error) {
		return wq.TaskAcknowledge(ctx, p)
	}))
	s.Register("wrkq.task.delete", apiHandler(func(ctx context.Context, p wrkqapi.TaskDeleteParams) (any, error) {
		return wq.TaskDelete(ctx, p)
	}))
	s.Register("wrkq.task.restore", apiHandler(func(ctx context.Context, p wrkqapi.TaskRestoreParams) (any, error) {
		return wq.TaskRestore(ctx, p)
	}))
	s.Register("wrkq.comment.add", apiHandler(func(ctx context.Context, p wrkqapi.CommentAddParams) (any, error) {
		return wq.CommentAdd(ctx, p)
	}))
	s.Register("wrkq.comment.catView", apiHandler(func(ctx context.Context, p wrkqapi.CommentCatViewParams) (any, error) {
		return wq.CommentCatView(ctx, p)
	}))
	s.Register("wrkq.comment.listView", apiHandler(func(ctx context.Context, p wrkqapi.CommentListViewParams) (any, error) {
		return wq.CommentListView(ctx, p)
	}))
	s.Register("wrkq.comment.list", apiHandler(func(ctx context.Context, p wrkqapi.CommentListParams) (any, error) {
		return wq.CommentList(ctx, p)
	}))
	s.Register("wrkq.comment.show", apiHandler(func(ctx context.Context, p wrkqapi.CommentShowParams) (any, error) {
		return wq.CommentShow(ctx, p)
	}))
	s.Register("wrkq.comment.delete", apiHandler(func(ctx context.Context, p wrkqapi.CommentDeleteParams) (any, error) {
		return wq.CommentDelete(ctx, p)
	}))
	s.Register("wrkq.attachment.add", apiHandler(func(ctx context.Context, p wrkqapi.AttachmentAddParams) (any, error) {
		return wq.AttachmentAdd(ctx, p)
	}))
	s.Register("wrkq.attachment.addBytes", apiHandler(func(ctx context.Context, p wrkqapi.AttachmentAddBytesParams) (any, error) {
		return wq.AttachmentAddBytes(ctx, p)
	}))
	s.Register("wrkq.attachment.getBytes", apiHandler(func(ctx context.Context, p wrkqapi.AttachmentGetBytesParams) (any, error) {
		return wq.AttachmentGetBytes(ctx, p)
	}))
	s.Register("wrkq.attachment.list", apiHandler(func(ctx context.Context, p wrkqapi.AttachmentListParams) (any, error) {
		return wq.AttachmentList(ctx, p)
	}))
	s.Register("wrkq.attachment.listView", apiHandler(func(ctx context.Context, p wrkqapi.AttachmentListViewParams) (any, error) {
		return wq.AttachmentListView(ctx, p)
	}))
	s.Register("wrkq.attachment.show", apiHandler(func(ctx context.Context, p wrkqapi.AttachmentShowParams) (any, error) {
		return wq.AttachmentShow(ctx, p)
	}))
	s.Register("wrkq.attachment.remove", apiHandler(func(ctx context.Context, p wrkqapi.AttachmentRemoveParams) (any, error) {
		return wq.AttachmentRemove(ctx, p)
	}))
	s.Register("wrkq.relation.add", apiHandler(func(ctx context.Context, p wrkqapi.RelationAddParams) (any, error) {
		return wq.RelationAdd(ctx, p)
	}))
	s.Register("wrkq.relation.listView", apiHandler(func(ctx context.Context, p wrkqapi.RelationListViewParams) (any, error) {
		return wq.RelationListView(ctx, p)
	}))
	s.Register("wrkq.relation.list", apiHandler(func(ctx context.Context, p wrkqapi.RelationListParams) (any, error) {
		return wq.RelationList(ctx, p)
	}))
	s.Register("wrkq.relation.remove", apiHandler(func(ctx context.Context, p wrkqapi.RelationRemoveParams) (any, error) {
		return wq.RelationRemove(ctx, p)
	}))
	s.Register("wrkq.container.create", apiHandler(func(ctx context.Context, p wrkqapi.ContainerCreateParams) (any, error) {
		return wq.ContainerCreate(ctx, p)
	}))
	s.Register("wrkq.container.update", apiHandler(func(ctx context.Context, p wrkqapi.ContainerUpdateParams) (any, error) {
		return wq.ContainerUpdate(ctx, p)
	}))
	s.Register("wrkq.container.move", apiHandler(func(ctx context.Context, p wrkqapi.ContainerMoveParams) (any, error) {
		return wq.ContainerMove(ctx, p)
	}))
	s.Register("wrkq.container.webhookSet", apiHandler(func(ctx context.Context, p wrkqapi.ContainerWebhookSetParams) (any, error) {
		return wq.ContainerWebhookSet(ctx, p)
	}))
	s.Register("wrkq.container.archive", apiHandler(func(ctx context.Context, p wrkqapi.ContainerArchiveParams) (any, error) {
		return wq.ContainerArchive(ctx, p)
	}))
	s.Register("wrkq.container.restore", apiHandler(func(ctx context.Context, p wrkqapi.ContainerRestoreParams) (any, error) {
		return wq.ContainerRestore(ctx, p)
	}))
	s.Register("wrkq.container.delete", apiHandler(func(ctx context.Context, p wrkqapi.ContainerDeleteParams) (any, error) {
		return wq.ContainerDelete(ctx, p)
	}))
	s.Register("wrkq.container.deleteRecursive", apiHandler(func(ctx context.Context, p wrkqapi.ContainerDeleteRecursiveParams) (any, error) {
		return wq.ContainerDeleteRecursive(ctx, p)
	}))
	s.Register("wrkq.container.show", apiHandler(func(ctx context.Context, p wrkqapi.ContainerShowParams) (any, error) {
		return wq.ContainerShow(ctx, p)
	}))
	s.Register("wrkq.container.catView", apiHandler(func(ctx context.Context, p wrkqapi.ContainerCatViewParams) (any, error) {
		return wq.ContainerCatView(ctx, p)
	}))
	s.Register("wrkq.container.list", apiHandler(func(ctx context.Context, p wrkqapi.ContainerListParams) (any, error) {
		return wq.ContainerList(ctx, p)
	}))
	s.Register("wrkq.project.listView", apiHandler(func(ctx context.Context, p wrkqapi.ProjectsListViewParams) (any, error) {
		return wq.ProjectsListView(ctx, p)
	}))
	s.Register("wrkq.project.setRoot", apiHandler(func(ctx context.Context, p wrkqapi.ProjectSetRootParams) (any, error) {
		return wq.ProjectSetRoot(ctx, p)
	}))
	s.Register("wrkq.webhook.add", apiHandler(func(ctx context.Context, p wrkqapi.WebhookMutateParams) (any, error) {
		return wq.WebhookAdd(ctx, p)
	}))
	s.Register("wrkq.webhook.remove", apiHandler(func(ctx context.Context, p wrkqapi.WebhookMutateParams) (any, error) {
		return wq.WebhookRemove(ctx, p)
	}))
	s.Register("wrkq.webhook.listView", apiHandler(func(ctx context.Context, p wrkqapi.WebhookListViewParams) (any, error) {
		return wq.WebhookListView(ctx, p)
	}))
	s.Register("wrkq.workflow.attach", apiHandler(func(ctx context.Context, p wrkqapi.WorkflowAttachParams) (any, error) {
		return wq.WorkflowAttach(ctx, p)
	}))
	s.Register("wrkq.workflow.inspect", apiHandler(func(ctx context.Context, p wrkqapi.WorkflowTaskParams) (any, error) {
		return wq.WorkflowInspect(ctx, p)
	}))
	s.Register("wrkq.workflow.timeline", apiHandler(func(ctx context.Context, p wrkqapi.WorkflowTaskParams) (any, error) {
		return wq.WorkflowTimeline(ctx, p)
	}))
	s.Register("wrkq.workflow.refresh", apiHandler(func(ctx context.Context, p taskActorParams) (any, error) {
		return wq.WorkflowRefresh(ctx, p.TaskSelector, defaultString(p.Actor, wrkqDefaultActor))
	}))

	// Handoff family (T-05117). Scope is CALLER-owned (resolved + self-scope
	// enforced by the mirror); the server receives EXPLICIT effective scope/actor
	// fields and never reads ASP_SCOPE_REF / ASP_HANDLE / ASP_AGENT_ID / ASP_PROJECT.
	s.Register("wrkq.handoff.create", apiHandler(func(ctx context.Context, p wrkqapi.HandoffCreateParams) (any, error) {
		return wq.HandoffCreate(ctx, p)
	}))
	s.Register("wrkq.handoff.get", apiHandler(func(ctx context.Context, p wrkqapi.HandoffGetParams) (any, error) {
		return wq.HandoffGet(ctx, p)
	}))
	s.Register("wrkq.handoff.listView", apiHandler(func(ctx context.Context, p wrkqapi.HandoffListViewParams) (any, error) {
		return wq.HandoffListView(ctx, p)
	}))
	s.Register("wrkq.handoff.searchView", apiHandler(func(ctx context.Context, p wrkqapi.HandoffSearchViewParams) (any, error) {
		return wq.HandoffSearchView(ctx, p)
	}))
	s.Register("wrkq.handoff.acknowledge", apiHandler(func(ctx context.Context, p wrkqapi.HandoffAcknowledgeParams) (any, error) {
		return wq.HandoffAcknowledge(ctx, p)
	}))
	// search + index family (T-05114). SERVER-OWNED: the wrkqapi search host opens +
	// migrates the derived sidecar, builds the host dense embedder, and (for index
	// update/rebuild) kickstarts ONLY this host's embedder via EnsureLlamaReady. The
	// mirror NEVER opens the sidecar or calls EnsureLlamaReady.
	s.Register("wrkq.search.listView", apiHandler(func(ctx context.Context, p wrkqapi.SearchListViewParams) (any, error) {
		return wq.SearchListView(ctx, p)
	}))
	s.Register("wrkq.index.status", apiHandler(func(ctx context.Context, p wrkqapi.IndexStatusParams) (any, error) {
		return wq.IndexStatus(ctx, p)
	}))
	s.Register("wrkq.index.update", apiHandler(func(ctx context.Context, p wrkqapi.IndexLifecycleParams) (any, error) {
		return wq.IndexUpdate(ctx, p)
	}))
	s.Register("wrkq.index.rebuild", apiHandler(func(ctx context.Context, p wrkqapi.IndexLifecycleParams) (any, error) {
		return wq.IndexRebuild(ctx, p)
	}))
	s.Register("wrkq.index.vacuum", apiHandler(func(ctx context.Context, p wrkqapi.IndexLifecycleParams) (any, error) {
		return wq.IndexVacuum(ctx, p)
	}))
	s.Register("wrkq.index.pause", apiHandler(func(ctx context.Context, p wrkqapi.IndexLifecycleParams) (any, error) {
		return wq.IndexPause(ctx, p)
	}))
	s.Register("wrkq.index.resume", apiHandler(func(ctx context.Context, p wrkqapi.IndexLifecycleParams) (any, error) {
		return wq.IndexResume(ctx, p)
	}))
}

func registerWrkfMethods(s *Server, api *wrkfapi.API, opts RegistryOptions) {
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
	s.Register("wrkf.workflow.discontinue", apiHandler(func(ctx context.Context, p templateLifecycleParams) (any, error) {
		return api.WorkflowDiscontinue(ctx, p.Ref, defaultString(p.PrincipalRef, opts.DefaultActor))
	}))
	s.Register("wrkf.workflow.reinstate", apiHandler(func(ctx context.Context, p templateLifecycleParams) (any, error) {
		return api.WorkflowReinstate(ctx, p.Ref)
	}))
	s.Register("wrkf.instance.show", apiHandler(func(ctx context.Context, p instanceParams) (any, error) {
		return api.InstanceShow(ctx, p.TaskSelector, p.InstanceID)
	}))
	s.Register("wrkf.instance.next", apiHandler(func(ctx context.Context, p instanceNextParams) (any, error) {
		return api.InstanceNext(ctx, p.TaskSelector, p.InstanceID, defaultString(p.Role, opts.DefaultRole))
	}))
	s.Register("wrkf.evidence.add", apiHandler(func(ctx context.Context, p wrkfapi.EvidenceAddParams) (any, error) {
		p.PrincipalRef = defaultString(p.PrincipalRef, opts.DefaultActor)
		p.Role = defaultString(p.Role, opts.DefaultRole)
		return api.EvidenceAdd(ctx, p)
	}))
	s.Register("wrkf.evidence.list", apiHandler(func(ctx context.Context, p instanceParams) (any, error) {
		return api.EvidenceList(ctx, p.TaskSelector, p.InstanceID)
	}))
	s.Register("wrkf.evidence.show", apiHandler(func(ctx context.Context, p idParams) (any, error) {
		return api.EvidenceShow(ctx, p.ID)
	}))
	s.Register("wrkf.evidence.suggest", apiHandler(func(ctx context.Context, p taskTransitionParams) (any, error) {
		return api.EvidenceSuggest(ctx, p.TaskSelector, p.Transition)
	}))
	s.Register("wrkf.ledger.append", apiHandler(func(ctx context.Context, p wrkfapi.LedgerAppendParams) (any, error) {
		p.WrittenBy = opts.DefaultActor
		return api.LedgerAppend(ctx, p)
	}))
	s.Register("wrkf.ledger.list", apiHandler(func(ctx context.Context, p wrkfapi.LedgerListParams) (any, error) {
		return api.LedgerList(ctx, p)
	}))
	s.Register("wrkf.event.query", apiHandler(func(ctx context.Context, p wrkfapi.EventQueryParams) (any, error) {
		return api.EventQuery(ctx, p)
	}))
	s.Register("wrkf.role.list", apiHandler(func(ctx context.Context, p wrkfapi.RoleListParams) (any, error) {
		return api.RoleList(ctx, p)
	}))
	s.Register("wrkf.role.bind", apiHandler(func(ctx context.Context, p wrkfapi.RoleBindParams) (any, error) {
		return api.RoleBind(ctx, p)
	}))
	s.Register("wrkf.role.unbind", apiHandler(func(ctx context.Context, p wrkfapi.RoleUnbindParams) (any, error) {
		return api.RoleUnbind(ctx, p)
	}))
	s.Register("wrkf.role.set", apiHandler(func(ctx context.Context, p wrkfapi.RoleSetParams) (any, error) {
		return api.RoleSet(ctx, p)
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
	s.Register("wrkf.suspension.resolve", apiHandler(func(ctx context.Context, p wrkfapi.SuspensionResolveParams) (any, error) {
		p.PrincipalRef = defaultString(p.PrincipalRef, opts.DefaultActor)
		p.Role = defaultString(p.Role, opts.DefaultRole)
		return api.SuspensionResolve(ctx, p)
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

// historyListViewHandler wires wrkq.history.listView, owning the server default
// limit = 50 (0 = unlimited). The default applies ONLY when the caller omits the
// `limit` key entirely; an explicit 0 stays unlimited. The mirror always sends the
// flag value (legacy default 50) so legacy byte parity holds regardless, while a
// raw RPC caller that omits limit still gets the documented server default.
func historyListViewHandler(wq *wrkqapi.API) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p wrkqapi.HistoryListViewParams
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		var probe struct {
			Limit *int `json:"limit"`
		}
		if len(raw) > 0 && string(raw) != "null" {
			_ = json.Unmarshal(raw, &probe)
		}
		if probe.Limit == nil {
			p.Limit = 50
		}
		result, err := wq.HistoryListView(ctx, p)
		if err != nil {
			return nil, err
		}
		return marshalResult(result)
	}
}

func apiHandler[P any](fn func(context.Context, P) (any, error)) HandlerFunc {
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
	if err := json.Unmarshal(raw, out); err != nil {
		return NewValidationError("invalid params", nil)
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

type templateLifecycleParams struct {
	Ref          string `json:"ref"`
	PrincipalRef string `json:"principal_ref,omitempty"`
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
	Actor        string `json:"actor,omitempty"`
}

type instanceParams struct {
	InstanceID   string `json:"instanceId,omitempty"`
	TaskSelector string `json:"task,omitempty"`
}

type instanceNextParams struct {
	InstanceID   string `json:"instanceId,omitempty"`
	TaskSelector string `json:"task,omitempty"`
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
