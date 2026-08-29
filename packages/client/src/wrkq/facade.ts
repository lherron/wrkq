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
  WrkqCampaignPortfolio,
  WrkqContainerCampaignActivateParams,
  WrkqContainerCampaignCloseParams,
  WrkqContainerCampaignConvertParams,
  WrkqContainerCampaignPortfolioParams,
  WrkqContainerCampaignUpdateParams,
  WrkqContainerTimelineView,
  WrkqContainerTimelineViewParams,
  WrkqContainerCreateParams,
  WrkqContainerDeleteParams,
  WrkqContainerDeleteRecursiveParams,
  WrkqContainerDeleteRecursiveResult,
  WrkqContainerDeleteResult,
  WrkqContainerListParams,
  WrkqContainerListResult,
  WrkqContainerTaskCounts,
  WrkqContainerTaskCountsParams,
  WrkqContainerShowParams,
  WrkqContainerUpdateParams,
  WrkqCampaignTransitionResult,
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
  WrkqRelation,
  WrkqRelationAddParams,
  WrkqRelationListParams,
  WrkqRelationListResult,
  WrkqRelationRemoveParams,
  WrkqProjectEntry,
  WrkqProjectListViewParams,
  WrkqProjectSetRootParams,
  WrkqProjectsListView,
  WrkqPromise,
  WrkqPromiseAddParams,
  WrkqPromiseDeleteParams,
  WrkqPromiseEditParams,
  WrkqPromiseListParams,
  WrkqPromiseListResult,
  WrkqPromiseReadyParams,
  WrkqPromiseRetargetParams,
  WrkqPromiseReviewParams,
  WrkqPromiseShowParams,
  WrkqEnvelope,
  WrkqEnvelopeAckParams,
  WrkqEnvelopeDeferParams,
  WrkqEnvelopeInboxView,
  WrkqEnvelopeInboxViewParams,
  WrkqEnvelopePendingView,
  WrkqEnvelopePendingViewParams,
  WrkqEnvelopePresentParams,
  WrkqEnvelopePresentResult,
  WrkqEnvelopeRoundParams,
  WrkqEnvelopeShowParams,
  WrkqEnvelopeBirthEnvelopeParams,
  WrkqEnvelopeBirth,
  WrkqRoom,
  WrkqRoomLabelParams,
  WrkqRoomListParams,
  WrkqRoomListResult,
  WrkqRoomLogView,
  WrkqRoomLogViewParams,
  WrkqRoomMemberParams,
  WrkqRoomMembersView,
  WrkqRoomMembersViewParams,
  WrkqRoomSayParams,
  WrkqRoomSayResult,
  WrkqRoomShowParams,
  WrkqSearchListView,
  WrkqSearchListViewParams,
  WrkqFindListView,
  WrkqFindListViewParams,
  WrkqTask,
  WrkqTaskAcknowledgeParams,
  WrkqTaskClaim,
  WrkqTaskClaimParams,
  WrkqTaskClaimValidateParams,
  WrkqTaskCopyParams,
  WrkqTaskCopyResult,
  WrkqTaskCreateParams,
  WrkqTaskDeleteParams,
  WrkqTaskListParams,
  WrkqTaskListResult,
  WrkqTaskMoveParams,
  WrkqTaskRestoreParams,
  WrkqTaskReleaseParams,
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
  WrkqWorkflowInstancesParams,
  WrkqWorkflowInstancesResult,
  WrkqWorkflowRefreshParams,
  WrkqWorkflowSyncMetaParams,
  WrkqWorkflowSyncMetaResult,
  WrkqWorkflowTimelineParams,
  WrkqWorkflowTimelineResult,
} from "./types.js";

export interface WrkqTaskFacade {
  create(params: WrkqTaskCreateParams): Promise<WrkqTask>;
  show(params: WrkqTaskShowParams): Promise<WrkqTask>;
  list(params?: WrkqTaskListParams): Promise<WrkqTaskListResult>;
  findListView(params?: WrkqFindListViewParams): Promise<WrkqFindListView>;
  update(params: WrkqTaskUpdateParams): Promise<WrkqTask>;
  claim(params: WrkqTaskClaimParams): Promise<WrkqTaskClaim>;
  claimValidate(params: WrkqTaskClaimValidateParams): Promise<WrkqTaskClaim>;
  release(params: WrkqTaskReleaseParams): Promise<WrkqTaskClaim>;
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

export interface WrkqPromiseFacade {
  add(params: WrkqPromiseAddParams): Promise<WrkqPromise>;
  show(params: WrkqPromiseShowParams): Promise<WrkqPromise>;
  list(params?: WrkqPromiseListParams): Promise<WrkqPromiseListResult>;
  ready(params?: WrkqPromiseReadyParams): Promise<WrkqPromiseListResult>;
  edit(params: WrkqPromiseEditParams): Promise<WrkqPromise>;
  renew(params: WrkqPromiseReviewParams): Promise<WrkqPromise>;
  resolve(params: WrkqPromiseReviewParams): Promise<WrkqPromise>;
  abandon(params: WrkqPromiseReviewParams): Promise<WrkqPromise>;
  attach(params: WrkqPromiseRetargetParams): Promise<WrkqPromise>;
  detach(params: WrkqPromiseRetargetParams): Promise<WrkqPromise>;
  delete(params: WrkqPromiseDeleteParams): Promise<WrkqPromise>;
}

export interface WrkqContainerFacade {
  create(params: WrkqContainerCreateParams): Promise<WrkqContainer>;
  update(params: WrkqContainerUpdateParams): Promise<WrkqContainer>;
  campaignConvert(
    params: WrkqContainerCampaignConvertParams,
  ): Promise<WrkqCampaignTransitionResult>;
  campaignActivate(
    params: WrkqContainerCampaignActivateParams,
  ): Promise<WrkqCampaignTransitionResult>;
  campaignUpdate(params: WrkqContainerCampaignUpdateParams): Promise<WrkqContainer>;
  campaignClose(
    params: WrkqContainerCampaignCloseParams,
  ): Promise<WrkqCampaignTransitionResult>;
  campaignPortfolio(
    params?: WrkqContainerCampaignPortfolioParams,
  ): Promise<WrkqCampaignPortfolio>;
  timelineView(params: WrkqContainerTimelineViewParams): Promise<WrkqContainerTimelineView>;
  delete(params: WrkqContainerDeleteParams): Promise<WrkqContainerDeleteResult>;
  deleteRecursive(
    params: WrkqContainerDeleteRecursiveParams,
  ): Promise<WrkqContainerDeleteRecursiveResult>;
  show(params: WrkqContainerShowParams): Promise<WrkqContainer>;
  list(params?: WrkqContainerListParams): Promise<WrkqContainerListResult>;
  taskCounts(params?: WrkqContainerTaskCountsParams): Promise<WrkqContainerTaskCounts>;
}

/** Top-level project discovery and host-portable checkout-root registry. */
export interface WrkqProjectFacade {
  listView(params?: WrkqProjectListViewParams): Promise<WrkqProjectsListView>;
  setRoot(params: WrkqProjectSetRootParams): Promise<WrkqProjectEntry>;
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
  instances(params: WrkqWorkflowInstancesParams): Promise<WrkqWorkflowInstancesResult>;
  timeline(params: WrkqWorkflowTimelineParams): Promise<WrkqWorkflowTimelineResult>;
  refresh(params: WrkqWorkflowRefreshParams): Promise<WrkqWorkflowInspectResult>;
  syncMeta(params?: WrkqWorkflowSyncMetaParams): Promise<WrkqWorkflowSyncMetaResult>;
}

/** Admin namespace placeholder; legacy actor admin methods were removed in T-05381. */
export interface WrkqAdminFacade {}

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

/**
 * wrkq.room.* — the collaboration ledger's rooms. A room is a durable
 * conversation keyed by a work identity, created lazily on the first `say`.
 * Rooms are readable by any principal: membership is identity and attendance,
 * never an ACL.
 *
 * Only `say({to})` fires. A say without `to` is a log entry and nobody is
 * presented; there is no mute and no subscription. Following a room is arming
 * `wrkq monitor watch <room-key>`, not a durable watch object.
 */
export interface WrkqRoomFacade {
  say(params: WrkqRoomSayParams): Promise<WrkqRoomSayResult>;
  show(params: WrkqRoomShowParams): Promise<WrkqRoom>;
  list(params?: WrkqRoomListParams): Promise<WrkqRoomListResult>;
  logView(params: WrkqRoomLogViewParams): Promise<WrkqRoomLogView>;
  /**
   * Set / clear the `hidden` discovery label. It changes what the DEFAULT
   * `list` returns and nothing else: a hidden room accepts says, delivers, and
   * gates turns exactly like any other. `close` and `reopen` are REMOVED —
   * rooms have no lifecycle, and the RPCs refuse with `room_lifecycle_removed`
   * for one burn-in window.
   */
  hide(params: WrkqRoomLabelParams): Promise<WrkqRoom>;
  unhide(params: WrkqRoomLabelParams): Promise<WrkqRoom>;
  join(params: WrkqRoomMemberParams): Promise<WrkqRoomMembersView>;
  leave(params: WrkqRoomMemberParams): Promise<WrkqRoomMembersView>;
  membersView(params: WrkqRoomMembersViewParams): Promise<WrkqRoomMembersView>;
}

/**
 * wrkq.envelope.* — one object for chat and obligation, addressed to exactly
 * one recipient.
 *
 * `present`, `pendingView`, and `roundEnded` are the HRC-FACING surface: the
 * kicker's wake set, the stop-hook predicate, and the redelivery bound. Nothing
 * else should call them. There is no agent-facing ack — for an agent the reply
 * IS the ack — so `ack` here is the operator verb, intended for a human
 * principal clearing dead mail.
 */
export interface WrkqEnvelopeFacade {
  show(params: WrkqEnvelopeShowParams): Promise<WrkqEnvelope>;
  inboxView(params?: WrkqEnvelopeInboxViewParams): Promise<WrkqEnvelopeInboxView>;
  defer(params: WrkqEnvelopeDeferParams): Promise<WrkqEnvelope>;
  ack(params: WrkqEnvelopeAckParams): Promise<WrkqRoomLogView>;
  present(params: WrkqEnvelopePresentParams): Promise<WrkqEnvelopePresentResult>;
  /**
   * The kicker wake set and the stop-hook predicate. `includeFyi` additionally
   * reports pending fyi envelopes in `items`; they never enter `blocking` and
   * never summon, so a consumer presents them only into a live generation.
   */
  pendingView(params?: WrkqEnvelopePendingViewParams): Promise<WrkqEnvelopePendingView>;
  roundEnded(params: WrkqEnvelopeRoundParams): Promise<WrkqEnvelope>;
  /**
   * The birth envelope of one target scope — HRC's registry host reads it to
   * designate, once, the node a virgin scope is born on. `null` when nothing
   * has ever fired at the scope.
   */
  birthEnvelope(params: WrkqEnvelopeBirthEnvelopeParams): Promise<WrkqEnvelopeBirth | null>;
}

export interface WrkqFacade {
  readonly task: WrkqTaskFacade;
  readonly comment: WrkqCommentFacade;
  readonly attachment: WrkqAttachmentFacade;
  readonly relation: WrkqRelationFacade;
  readonly promise: WrkqPromiseFacade;
  readonly room: WrkqRoomFacade;
  readonly envelope: WrkqEnvelopeFacade;
  readonly container: WrkqContainerFacade;
  readonly project: WrkqProjectFacade;
  readonly webhook: WrkqWebhookFacade;
  readonly workflow: WrkqWorkflowFacade;
  readonly handoff: WrkqHandoffFacade;
  readonly search: WrkqSearchFacade;
  readonly index: WrkqIndexFacade;
  readonly admin: WrkqAdminFacade;
}
