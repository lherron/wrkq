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
  WrkqTask,
  WrkqTaskAcknowledgeParams,
  WrkqTaskCreateParams,
  WrkqTaskDeleteParams,
  WrkqTaskListParams,
  WrkqTaskListResult,
  WrkqTaskRestoreParams,
  WrkqTaskShowParams,
  WrkqTaskUpdateParams,
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
  acknowledge(params: WrkqTaskAcknowledgeParams): Promise<WrkqTask>;
  delete(params: WrkqTaskDeleteParams): Promise<WrkqTask>;
  restore(params: WrkqTaskRestoreParams): Promise<WrkqTask>;
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
  delete(params: WrkqContainerDeleteParams): Promise<WrkqContainerDeleteResult>;
  deleteRecursive(
    params: WrkqContainerDeleteRecursiveParams,
  ): Promise<WrkqContainerDeleteRecursiveResult>;
  show(params: WrkqContainerShowParams): Promise<WrkqContainer>;
  list(params?: WrkqContainerListParams): Promise<WrkqContainerListResult>;
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

export interface WrkqFacade {
  readonly task: WrkqTaskFacade;
  readonly comment: WrkqCommentFacade;
  readonly attachment: WrkqAttachmentFacade;
  readonly relation: WrkqRelationFacade;
  readonly container: WrkqContainerFacade;
  readonly workflow: WrkqWorkflowFacade;
  readonly admin: WrkqAdminFacade;
}
