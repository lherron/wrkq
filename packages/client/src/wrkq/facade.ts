/**
 * wrkq/facade.ts — typed surface for the wrkq namespace (task ownership).
 *
 * This is a pure type. The concrete implementation lives in the root client
 * factory (src/client.ts). Subpath export `@wrkq/client/wrkq` re-exports this
 * type plus the wrkq DTOs.
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2.
 */

import type {
  WrkqAttachment,
  WrkqAttachmentAddParams,
  WrkqAttachmentListParams,
  WrkqAttachmentListResult,
  WrkqAttachmentRemoveParams,
  WrkqAttachmentShowParams,
  WrkqBundleExportView,
  WrkqBundleExportViewParams,
  WrkqComment,
  WrkqCommentAddParams,
  WrkqCommentDeleteParams,
  WrkqCommentListParams,
  WrkqCommentListResult,
  WrkqCommentShowParams,
  WrkqContainer,
  WrkqContainerCreateParams,
  WrkqContainerDeleteParams,
  WrkqContainerDeleteRecursiveParams,
  WrkqContainerDeleteRecursiveResult,
  WrkqContainerDeleteResult,
  WrkqContainerListParams,
  WrkqContainerListResult,
  WrkqContainerShowParams,
  WrkqContainerUpdateParams,
  WrkqHandoff,
  WrkqHandoffAcknowledgeParams,
  WrkqHandoffCreateParams,
  WrkqHandoffCreateResult,
  WrkqHandoffGetParams,
  WrkqHandoffListResult,
  WrkqHandoffListViewParams,
  WrkqIndexLifecycleParams,
  WrkqIndexRebuildResult,
  WrkqIndexStateResult,
  WrkqIndexStatus,
  WrkqIndexUpdateResult,
  WrkqIndexVacuumResult,
  WrkqLegacyActor,
  WrkqLegacyActorCreateParams,
  WrkqLegacyActorListParams,
  WrkqLegacyActorListResult,
  WrkqLegacyActorUpdateParams,
  WrkqRelation,
  WrkqRelationAddParams,
  WrkqRelationListParams,
  WrkqRelationListResult,
  WrkqRelationRemoveParams,
  WrkqSearchListView,
  WrkqSearchListViewParams,
  WrkqTask,
  WrkqTaskAcknowledgeParams,
  WrkqTaskCopyParams,
  WrkqTaskCopyResult,
  WrkqTaskCreateParams,
  WrkqTaskDeleteParams,
  WrkqTaskListParams,
  WrkqTaskListResult,
  WrkqTaskMoveParams,
  WrkqTaskRestoreParams,
  WrkqTaskShowParams,
  WrkqTaskUpdateParams,
  WrkqWebhookListViewParams,
  WrkqWebhookMutateParams,
  WrkqWebhookMutateResult,
  WrkqWebhookRow,
  WrkqWorkflowAttachParams,
  WrkqWorkflowAttachResult,
  WrkqWorkflowInspectParams,
  WrkqWorkflowInspectResult,
  WrkqWorkflowRefreshParams,
  WrkqWorkflowTimelineParams,
  WrkqWorkflowTimelineResult,
} from "./types.js";

export interface WrkqTaskFacade {
  create(params: WrkqTaskCreateParams): Promise<WrkqTask>;
  show(params: WrkqTaskShowParams): Promise<WrkqTask>;
  list(params?: WrkqTaskListParams): Promise<WrkqTaskListResult>;
  update(params: WrkqTaskUpdateParams): Promise<WrkqTask>;
  move(params: WrkqTaskMoveParams): Promise<WrkqTask>;
  acknowledge(params: WrkqTaskAcknowledgeParams): Promise<WrkqTask>;
  delete(params: WrkqTaskDeleteParams): Promise<WrkqTask>;
  restore(params: WrkqTaskRestoreParams): Promise<WrkqTask>;
  copy(params: WrkqTaskCopyParams): Promise<WrkqTaskCopyResult>;
}

export interface WrkqCommentFacade {
  add(params: WrkqCommentAddParams): Promise<WrkqComment>;
  list(params: WrkqCommentListParams): Promise<WrkqCommentListResult>;
  show(params: WrkqCommentShowParams): Promise<WrkqComment>;
  delete(params: WrkqCommentDeleteParams): Promise<WrkqComment>;
}

export interface WrkqAttachmentFacade {
  add(params: WrkqAttachmentAddParams): Promise<WrkqAttachment>;
  list(params: WrkqAttachmentListParams): Promise<WrkqAttachmentListResult>;
  show(params: WrkqAttachmentShowParams): Promise<WrkqAttachment>;
  remove(params: WrkqAttachmentRemoveParams): Promise<WrkqAttachment>;
}

export interface WrkqRelationFacade {
  add(params: WrkqRelationAddParams): Promise<WrkqRelation>;
  list(params: WrkqRelationListParams): Promise<WrkqRelationListResult>;
  remove(params: WrkqRelationRemoveParams): Promise<WrkqRelation>;
}

export interface WrkqContainerFacade {
  create(params: WrkqContainerCreateParams): Promise<WrkqContainer>;
  update(params: WrkqContainerUpdateParams): Promise<WrkqContainer>;
  delete(params: WrkqContainerDeleteParams): Promise<WrkqContainerDeleteResult>;
  deleteRecursive(
    params: WrkqContainerDeleteRecursiveParams,
  ): Promise<WrkqContainerDeleteRecursiveResult>;
  show(params: WrkqContainerShowParams): Promise<WrkqContainer>;
  list(params?: WrkqContainerListParams): Promise<WrkqContainerListResult>;
}

/**
 * Global webhook subscriptions on the singleton root container (DEDICATED family,
 * T-05119). add/remove are PRODUCER mutations; listView is a CLI compatibility
 * list projection. NOT wrkq.container.update (which rejects webhookUrls).
 */
export interface WrkqWebhookFacade {
  add(params: WrkqWebhookMutateParams): Promise<WrkqWebhookMutateResult>;
  remove(params: WrkqWebhookMutateParams): Promise<WrkqWebhookMutateResult>;
  listView(params?: WrkqWebhookListViewParams): Promise<WrkqWebhookRow[]>;
}

export interface WrkqWorkflowFacade {
  attach(params: WrkqWorkflowAttachParams): Promise<WrkqWorkflowAttachResult>;
  inspect(params: WrkqWorkflowInspectParams): Promise<WrkqWorkflowInspectResult>;
  timeline(params: WrkqWorkflowTimelineParams): Promise<WrkqWorkflowTimelineResult>;
  refresh(params: WrkqWorkflowRefreshParams): Promise<WrkqWorkflowInspectResult>;
}

/**
 * Legacy actor read/admin compatibility surface (wrkq.admin.legacyActor.*).
 *
 * Actor rows are a LEGACY display/admin cache: these methods never issue,
 * validate, or authorize principals, and no actor row is a prerequisite for any
 * task/comment/container/workflow write. Use principal-based attribution for new
 * work; this facade exists for taskboard's admin actors page and display
 * enrichment.
 */
export interface WrkqLegacyActorFacade {
  list(params?: WrkqLegacyActorListParams): Promise<WrkqLegacyActorListResult>;
  create(params: WrkqLegacyActorCreateParams): Promise<WrkqLegacyActor>;
  update(params: WrkqLegacyActorUpdateParams): Promise<WrkqLegacyActor>;
}

/** Admin/legacy compatibility namespace (wrkq.admin.*). */
export interface WrkqAdminFacade {
  readonly legacyActor: WrkqLegacyActorFacade;
}

/**
 * Bundle namespace (wrkq.bundle.*). exportView returns the server-owned LOGICAL
 * bundle snapshot (read under one transaction); the CALLER materializes the
 * bundle directory from it. The snapshot carries NO server-host paths and NO
 * attachment bytes (descriptors only). Mirrors docs/wrkq-wrkf-rpc.md §6.2.
 */
export interface WrkqBundleFacade {
  exportView(params: WrkqBundleExportViewParams): Promise<WrkqBundleExportView>;
}

/**
 * Handoff family (wrkq.handoff.*). Scope is CALLER-owned but NOT project-root:
 * callers resolve the effective agent/project scope (and enforce self-scope for
 * create) and pass EXPLICIT scope/actor fields. The server reads no agent-runtime
 * env. searchView is DEFERRED until the search/index slice lands.
 */
export interface WrkqHandoffFacade {
  create(params: WrkqHandoffCreateParams): Promise<WrkqHandoffCreateResult>;
  get(params: WrkqHandoffGetParams): Promise<WrkqHandoff>;
  listView(params: WrkqHandoffListViewParams): Promise<WrkqHandoffListResult>;
  acknowledge(params: WrkqHandoffAcknowledgeParams): Promise<WrkqHandoff>;
}

/**
 * wrkq.search.* — the server-owned search read surface (T-05114). The SERVER owns
 * the derived sidecar + dense embedder; the client owns ONLY project-root path
 * scoping (paths pre-scoped) + presentation, NEVER the sidecar.
 */
export interface WrkqSearchFacade {
  listView(params: WrkqSearchListViewParams): Promise<WrkqSearchListView>;
}

/**
 * wrkq.index.* — the server-owned search index lifecycle (T-05114). update/rebuild
 * kickstart ONLY the server host's configured embedder (EnsureLlamaReady) — never
 * the caller's. The client owns presentation only.
 */
export interface WrkqIndexFacade {
  status(): Promise<WrkqIndexStatus>;
  update(params?: WrkqIndexLifecycleParams): Promise<WrkqIndexUpdateResult>;
  rebuild(params?: WrkqIndexLifecycleParams): Promise<WrkqIndexRebuildResult>;
  vacuum(params?: WrkqIndexLifecycleParams): Promise<WrkqIndexVacuumResult>;
  pause(params?: WrkqIndexLifecycleParams): Promise<WrkqIndexStateResult>;
  resume(params?: WrkqIndexLifecycleParams): Promise<WrkqIndexStateResult>;
}

export interface WrkqFacade {
  readonly task: WrkqTaskFacade;
  readonly comment: WrkqCommentFacade;
  readonly attachment: WrkqAttachmentFacade;
  readonly relation: WrkqRelationFacade;
  readonly container: WrkqContainerFacade;
  readonly webhook: WrkqWebhookFacade;
  readonly workflow: WrkqWorkflowFacade;
  readonly handoff: WrkqHandoffFacade;
  readonly search: WrkqSearchFacade;
  readonly index: WrkqIndexFacade;
  readonly admin: WrkqAdminFacade;
  readonly bundle: WrkqBundleFacade;
}
