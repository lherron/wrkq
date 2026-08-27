/**
 * wrkq/types.ts — DTOs for the wrkq namespace (tasks, comments, attachments,
 * relations, containers, task-workflow binding).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7. All RPC DTO JSON fields are
 * camelCase. Field sets verified against the live `wrkq rpc --stdio` server
 * (proto 2026-06-30).
 */

import type { WrkfEvent, WrkfInstance } from "../wrkf/types.js";

export type WrkqTaskState =
  | "idea"
  | "draft"
  | "open"
  | "in_progress"
  | "completed"
  | "blocked"
  | "cancelled"
  | "archived"
  | "deleted";

export type WrkqTaskKind = "task" | "subtask" | "spike" | "bug" | "chore";
export type WrkqRiskClass = "low" | "medium" | "high" | string;

// ── Promise ──────────────────────────────────────────────────────────────────

export type WrkqPromiseState = "open" | "resolved" | "abandoned";

export interface WrkqPromiseSubjectRef {
  type: "task" | "container";
  uuid: string;
  id: string;
  path: string;
}

export interface WrkqPromise {
  uuid: string;
  id: string;
  ownerPrincipalRef: string;
  subject: string;
  reviewQuestion?: string;
  subjectRef: WrkqPromiseSubjectRef | null;
  reviewAt: string;
  ready: boolean;
  readyFor?: string;
  state: WrkqPromiseState;
  closedAt?: string;
  lastReviewedAt?: string;
  lastReviewNote?: string;
  meta: Record<string, unknown>;
  etag: number;
  createdAt: string;
  updatedAt: string;
  createdByPrincipalRef: string;
  updatedByPrincipalRef: string;
}

export interface WrkqPromiseAddParams {
  ownerPrincipalRef?: string;
  onBehalf?: boolean;
  subject?: string;
  reviewQuestion?: string;
  task?: string;
  container?: string;
  reviewAt?: string;
  reviewIn?: string;
  meta?: Record<string, unknown>;
  principalRef?: string;
}

export interface WrkqPromiseShowParams {
  promise: string;
}

export interface WrkqPromiseListParams {
  ownerPrincipalRef?: string;
  state?: WrkqPromiseState | "all";
  task?: string;
  container?: string;
  principalRef?: string;
}

export interface WrkqPromiseReadyParams {
  ownerPrincipalRef?: string;
  project?: string;
  includeGlobal?: boolean;
  principalRef?: string;
}

export interface WrkqPromiseEditParams {
  promise: string;
  subject?: string;
  reviewQuestion?: string;
  reviewAt?: string;
  reviewIn?: string;
  meta?: Record<string, unknown>;
  ifMatch?: number;
  principalRef?: string;
}

export interface WrkqPromiseReviewParams {
  promise: string;
  reviewAt?: string;
  reviewIn?: string;
  note?: string;
  ifMatch?: number;
  principalRef?: string;
}

export interface WrkqPromiseRetargetParams {
  promise: string;
  task?: string;
  container?: string;
  ifMatch?: number;
  principalRef?: string;
}

export interface WrkqPromiseDeleteParams {
  promise: string;
  mode?: "soft" | "abandon" | "purge";
  ifMatch?: number;
  principalRef?: string;
}

export interface WrkqPromiseListResult {
  items: WrkqPromise[];
}

// ── Collaboration ledger: rooms and envelopes ────────────────────────────────
//
// wrkq owns collaboration; HRC is a consumer (T-07612 §2). Every HRC identifier
// carried here — node, runtimeId, hostSessionId, generation, runId — is an
// OPAQUE STRING to wrkq. It never interprets one and never imports hrc.

export type WrkqRoomKind = "campaign" | "task" | "project" | "adhoc";
export type WrkqRoomState = "open" | "closed" | "archived";
export type WrkqEnvelopeObligation = "reply_required" | "fyi" | "none";
export type WrkqEnvelopeState =
  | "pending"
  | "presented"
  | "acked"
  | "deferred"
  | "dead";
export type WrkqRoomMemberSource = "spoke" | "addressed" | "joined";

export interface WrkqRoomWorkRef {
  type: "task" | "container";
  uuid: string;
  id: string;
  path: string;
}

/**
 * The other room a task/campaign pair holds. A task that later joins a campaign
 * routes new says to the campaign room while its own room stays readable —
 * linked both ways, never merged.
 */
export interface WrkqRoomLink {
  relation: string;
  key: string;
  uuid: string;
  kind: WrkqRoomKind;
}

export interface WrkqRoom {
  uuid: string;
  /** Ad-hoc rooms only; a derived room's key IS its work identity. */
  id?: string;
  /** The room key: `T-xxxxx`, a container path, or `R-xxxxx`. */
  key: string;
  kind: WrkqRoomKind;
  subject?: string;
  /** The EFFECTIVE state a caller must obey. */
  state: WrkqRoomState;
  /**
   * The durable column. When it differs from `state`, the closure is DERIVED
   * (the task went terminal, the campaign closed) rather than explicit.
   */
  storedState: WrkqRoomState;
  workRef: WrkqRoomWorkRef | null;
  links: WrkqRoomLink[];
  openedByPrincipalRef: string;
  openedAt: string;
  closedAt?: string;
  lastActivityAt: string;
  memberCount: number;
  messageCount: number;
  etag: number;
  createdAt: string;
  updatedAt: string;
}

/** One end of an envelope. `scopeRef` is absent for a scope-less principal. */
export interface WrkqEnvelopeParty {
  principalRef: string;
  scopeRef?: string;
}

/** One presentation receipt: the join between wrkq and HRC's execution world. */
export interface WrkqEnvelopePresentation {
  memberRef: string;
  node?: string;
  runtimeId?: string;
  hostSessionId?: string;
  generation?: string;
  runId?: string;
  driveAttemptId?: string;
  presentedAt: string;
}

export interface WrkqEnvelope {
  uuid: string;
  /** `EN-xxxxx`. An INTERNAL row id: the injected presentation never shows it. */
  id: string;
  roomUuid: string;
  roomKey: string;
  roomKind: WrkqRoomKind;
  /** Shared by the envelopes one say fanned out to. */
  groupId?: string;
  from: WrkqEnvelopeParty;
  to: WrkqEnvelopeParty | null;
  obligation: WrkqEnvelopeObligation;
  body: string;
  /** Set when the say routed via a task, even into a campaign room. */
  taskId?: string;
  state: WrkqEnvelopeState;
  /** acked | dead. `deferred` is paused, NEVER terminal. */
  terminal: boolean;
  roundCount: number;
  retryAt?: string;
  deferReason?: string;
  terminalActor?: string;
  urgent: boolean;
  materializationIntent?: string;
  respondToPrincipalRef?: string;
  retryPromiseId?: string;
  /** The SAY's key, carried by EVERY envelope of a fan-out. */
  idempotencyKey?: string;
  meta: Record<string, unknown>;
  presentedTo: WrkqEnvelopePresentation[];
  etag: number;
  createdAt: string;
  updatedAt: string;
}

export interface WrkqRoomMember {
  memberRef: string;
  memberPrincipalRef: string;
  scoped: boolean;
  source: WrkqRoomMemberSource;
  joinedAt: string;
  leftAt?: string;
  /** Scope-less members have none: they are never presented through a runtime. */
  attendance: WrkqEnvelopePresentation | null;
}

export interface WrkqRoomSayParams {
  /** Routed per T-07612 §4: R-/EN-, T-, container, or agent@project[:task]. */
  ref?: string;
  body: string;
  /** Fans out to one envelope per addressee. Only `to` fires. */
  to?: string[];
  fyi?: boolean;
  subject?: string;
  /** Force a fresh ad-hoc room instead of reusing the open pair room. */
  new?: boolean;
  urgent?: boolean;
  respondTo?: string;
  /** Also write the body as a wrkq comment on the room's task. */
  record?: boolean;
  idempotencyKey?: string;
  meta?: Record<string, unknown>;
  principalRef?: string;
  /** The caller's own HRC session handle, when it has one. */
  scopeRef?: string;
}

export interface WrkqRoomSayResult {
  room: WrkqRoom;
  /** The waitable handle; equals the envelope's own id for one addressee. */
  groupId: string;
  envelopes: WrkqEnvelope[];
  /** Envelope ids this say discharged under reply-is-ack. */
  acked: string[];
  recordedCommentId?: string;
}

export interface WrkqRoomOpenParams {
  members: string[];
  subject: string;
  task?: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomShowParams {
  room: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomListParams {
  state?: WrkqRoomState | "all";
  kind?: WrkqRoomKind;
  /** "me" restricts to rooms the caller's own scope is an active member of. */
  scope?: "me";
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomListResult {
  items: WrkqRoom[];
}

export interface WrkqRoomLogViewParams {
  room: string;
  /** Narrow a campaign room to the traffic that came through one task. */
  task?: string;
  /** Return only the newest N messages, still oldest-first. */
  limit?: number;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomLogView {
  room: WrkqRoom;
  items: WrkqEnvelope[];
}

export interface WrkqRoomLifecycleParams {
  room: string;
  ifMatch?: number;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomMemberParams {
  room: string;
  /** Omit on join/leave to mean the caller's own scope. */
  member?: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomMembersViewParams {
  room: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomMembersView {
  room: WrkqRoom;
  items: WrkqRoomMember[];
}

export interface WrkqEnvelopeShowParams {
  envelope: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqEnvelopeInboxViewParams {
  scopeRef?: string;
  includeDead?: boolean;
  principalRef?: string;
}

export interface WrkqEnvelopeInboxGroup {
  room: WrkqRoom;
  items: WrkqEnvelope[];
}

/**
 * fyi is never listed here: it carries no obligation.
 *
 * An obligation in a CLOSED room is still listed — closure does not retire it —
 * even though `pendingView` drops it. Key off `group.room.state !== "open"` to
 * render it apart from the obligations that actually gate a turn.
 */
export interface WrkqEnvelopeInboxView {
  scopeRef?: string;
  principalRef: string;
  groups: WrkqEnvelopeInboxGroup[];
  deferred: WrkqEnvelope[];
  dead: WrkqEnvelope[];
}

export interface WrkqEnvelopeDeferParams {
  envelope: string;
  reason: string;
  /** Relative retry time resolved by the server; backed by a wrkq promise. */
  retryAfter?: string;
  retryAt?: string;
  ifMatch?: number;
  principalRef?: string;
  scopeRef?: string;
}

/** OPERATOR-only. For an agent, the reply IS the ack. */
export interface WrkqEnvelopeAckParams {
  envelopes: string[];
  note?: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqEnvelopePresentParams {
  envelope: string;
  memberRef?: string;
  node?: string;
  runtimeId?: string;
  hostSessionId?: string;
  generation?: string;
  runId?: string;
  /** One drive attempt presents an envelope exactly once. */
  driveAttemptId?: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqEnvelopePresentResult {
  envelope: WrkqEnvelope;
  recorded: boolean;
  /**
   * The §7 `history:` cue decision, keyed to the RUNTIME and not the
   * generation: /quit clears continuation without rotating the generation, so
   * every post-quit runtime is cold and gets the cue.
   */
  historyHint: boolean;
  messageCount: number;
  lastMessageAt?: string;
}

export interface WrkqEnvelopePendingViewParams {
  scopes?: string[];
  /**
   * Additionally report pending `fyi` envelopes in `items`. A request
   * parameter, not a feature flag: the default read is the wake set and stays
   * obligation-only. A fyi never enters `blocking` and never summons — gating
   * presentation to a live generation is the consumer's half of §5.
   */
  includeFyi?: boolean;
  principalRef?: string;
  scopeRef?: string;
}

/**
 * The kicker wake set AND the stop-hook predicate in one read model.
 *
 * Envelopes whose room reads `closed` or `archived` are absent from BOTH
 * `items` and `blocking`: a closed room refuses a say, so the addressee has no
 * reply path and there is nothing a summoned turn could do. They are NOT
 * retired — `inboxView` still lists them under their closed room — and
 * reopening the room returns them to this read unchanged.
 */
export interface WrkqEnvelopePendingView {
  /**
   * Standing reply_required envelopes, plus pending `fyi` envelopes when the
   * caller asked for `includeFyi`.
   */
  items: WrkqEnvelope[];
  /** Envelope ids that must be replied or deferred before a turn may end. */
  blocking: string[];
  /** How many due deferrals this read's sweep returned to pending. */
  repended: number;
}

export interface WrkqEnvelopeRoundParams {
  envelope: string;
  maxRounds?: number;
  principalRef?: string;
  scopeRef?: string;
}

// ── Task ─────────────────────────────────────────────────────────────────────

export interface WrkqTaskCreateParams {
  path?: string;
  project?: string;
  title: string;
  description?: string;
  specification?: string;
  kind?: WrkqTaskKind;
  priority?: number;
  state?: WrkqTaskState;
  parentTask?: string;
  assigneePrincipalRef?: string | null;
  labels?: string[];
  meta?: Record<string, unknown>;
  riskClass?: WrkqRiskClass;
  principalRef?: string;
  idempotencyKey?: string;
}

export interface WrkqTaskShowParams {
  task: string;
}

export interface WrkqTaskListParams {
  path?: string;
  state?: WrkqTaskState | WrkqTaskState[];
  kind?: string | string[];
  assignee?: string;
  claimedBy?: string;
  claimedNode?: string;
  labels?: string[];
  includeDeleted?: boolean;
  limit?: number;
  cursor?: string;
  /** Sort field. Whitelist: created_at (default), updated_at, priority, id, path. */
  sort?: "created_at" | "updated_at" | "priority" | "id" | "path";
  /** Sort direction; defaults to ascending. */
  direction?: "asc" | "desc";
  /** Include tasks in containers nested under `path` (the whole subtree). Default false. */
  recursive?: boolean;
  /** Omit description/specification bodies from list items while retaining presence booleans. */
  summary?: boolean;
}

export interface WrkqTaskUpdateParams {
  task: string;
  patch: {
    title?: string;
    description?: string;
    specification?: string;
    /** Curated plain-terms result; blank/whitespace clears it. */
    outcome?: string;
    state?: WrkqTaskState;
    priority?: number;
    kind?: string;
    labels?: string[];
    meta?: Record<string, unknown>;
    riskClass?: WrkqRiskClass;
    assigneePrincipalRef?: string | null;
    dueAt?: string | null;
		startAt?: string | null;
		/** Campaign ID/path to enroll; empty string unenrolls. */
		campaign?: string;
  };
  /** CAS precondition; see docs/wrkq-wrkf-rpc.md §8.1. */
  expectEtag?: number;
  idempotencyKey?: string;
  /** Required with claimGeneration/claimToken when completing a claimed task. */
  claimScope?: string;
  claimGeneration?: number;
  claimToken?: string;
}

export interface WrkqTaskClaimParams {
  task: string;
  principalRef: string;
  /** Exact task-scoped agent sessionRef. */
  scope: string;
  /** Explicit noninteractive takeover intent; callers own confirmation. */
  takeOver?: boolean;
}

export interface WrkqTaskClaimValidateParams {
  task: string;
  principalRef: string;
  scope: string;
  claimGeneration: number;
  claimToken: string;
}

export interface WrkqTaskReleaseParams {
  task: string;
  principalRef: string;
  scope?: string;
  claimGeneration?: number;
  claimToken?: string;
  /** Operator release without holder authority; callers own confirmation. */
  force?: boolean;
}

export interface WrkqTaskClaim {
  task: string;
  claimedBy: string;
  claimedScope: string;
  /** Derived by wrkqd from the authenticated per-node bearer. */
  claimedNode: string;
  claimedAt: string;
  claimGeneration: number;
  /** Returned only when a claim/takeover mints fresh authority. */
  claimToken?: string;
}

export interface WrkqTaskMoveParams {
  task: string;
  targetPath: string;
  /** Root task CAS precondition. */
  expectEtag?: number;
}

export interface WrkqTaskAcknowledgeParams {
  task: string;
  /** Allow ack on non-terminal tasks (mirrors `wrkq ack --force`). */
  force?: boolean;
}

export interface WrkqTaskDeleteParams {
  task: string;
}

/**
 * WrkqTaskRestoreParams carries the WHOLE legacy `wrkq restore` semantic op
 * SERVER-side (caller-owned-confirmation B-ruling, T-05100): move-on-restore,
 * field updates, comment, etag precondition — never composed client-side. Empty/
 * zero field values mean "leave unchanged" (legacy semantics). Mirrors
 * docs/wrkq-wrkf-rpc.md §6.2 WrkqTaskRestoreParams.
 */
export interface WrkqTaskRestoreParams {
  task: string;
  /** Target state (default "open"); archived/deleted targets are rejected. */
  state?: string;
  /** Move-on-restore destination (parent container path + final slug). */
  toPath?: string;
  /** Field update on restore (empty = unchanged). */
  title?: string;
  /** Field update on restore (empty = unchanged). */
  description?: string;
  /** Field update on restore (1-4; 0/omitted = unchanged). */
  priority?: number;
  /** JSON array string; field update on restore ("" = unchanged). */
  labels?: string;
  /** Compat actor/principal ref; field update on restore. */
  assignee?: string;
  /** Appended as a comment on restore. */
  comment?: string;
  /** Conditional etag precondition; mismatch → WRKQ_CONFLICT. */
  ifMatch?: number;
}

/**
 * WrkqTaskCopyParams selects ONE source task + a destination container and the
 * copy options. The server owns the per-source deep copy; the CLI owns
 * multi-source fan-out / prompts / dry-run / output. Mirrors
 * docs/wrkq-wrkf-rpc.md §6.2 (T-05111, daedalus hrcchat#10196).
 */
export interface WrkqTaskCopyParams {
  source: string;
  destination: string;
  overwrite?: boolean;
  withAttachments?: boolean;
  shallow?: boolean;
  /** Source-task etag CAS precondition. */
  expectEtag?: number;
  actor?: string;
  /** Mandatory-style under client fan-out: a retried copy must not duplicate. */
  idempotencyKey?: string;
}

/**
 * WrkqTaskCopyResult is the per-source copy outcome.
 *
 * Keys are DELIBERATELY snake_case — they are the LEGACY `copyResult` output
 * keys, preserved verbatim for byte-parity with legacy `wrkq cp` machine output.
 * Do NOT camelCase them.
 */
export interface WrkqTaskCopyResult {
  source_id: string;
  source_uuid: string;
  dest_id: string;
  dest_uuid: string;
  dest_path: string;
  attachments_copied?: number;
  with_files?: boolean;
}

export interface WrkqTask {
  uuid: string;
  id: string;
  slug: string;
  title: string;
	projectUuid: string;
	campaignUuid?: string;
  path: string;
  state: WrkqTaskState;
  priority: number;
  kind: string;
  description: string;
  specification: string;
  /** Curated plain-terms result of the work. */
  outcome?: string;
  hasDescription?: boolean;
  hasSpecification?: boolean;
  labels: string[];
  meta: Record<string, unknown>;
  riskClass?: WrkqRiskClass;
  etag: number;
  startAt?: string;
  dueAt?: string;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
  archivedAt?: string;
  deletedAt?: string;
  acknowledgedAt?: string;
  assigneePrincipalRef?: string;
  claimedBy?: string;
  claimedScope?: string;
  claimedNode?: string;
  claimedAt?: string;
  claimGeneration?: number;
  createdByPrincipalRef?: string;
  updatedByPrincipalRef?: string;
}

export interface WrkqTaskListResult {
  items: WrkqTask[];
  nextCursor?: string;
}

/**
 * Params for wrkq.task.findListView, the server-owned CLI compatibility
 * projection. `labels` uses exact, case-sensitive membership and requires every
 * requested value; duplicate values are idempotent.
 */
export interface WrkqFindListViewParams {
  paths?: string[];
  type?: "t" | "p";
  slugGlob?: string;
  state?: string;
  dueBefore?: string;
  dueAfter?: string;
  kind?: string;
  labels?: string[];
  assignee?: string;
  claimedBy?: string;
  claimedNode?: string;
  parentTask?: string;
  requestedBy?: string;
  assignedProject?: string;
  causedBy?: string;
  ackPending?: boolean;
  hasOutcome?: boolean;
  campaign?: string;
  limit?: number;
  cursor?: string;
  sort?: "updated_at" | "created_at" | "id" | "path";
  reverse?: boolean;
}

/** One legacy-shaped task/container row returned by wrkq.task.findListView. */
export interface WrkqFindEntry {
  type: "task" | "container";
  uuid: string;
  id: string;
  slug: string;
  title: string;
  path: string;
  specification?: string;
  state?: string;
  priority?: number;
  kind?: string;
  assignee?: string;
  assignee_principal_ref?: string;
  claimed_by?: string;
  claimed_scope?: string;
  claimed_node?: string;
  claimed_at?: string;
  claim_generation?: number;
  parent_task_id?: string;
  requested_by_project_id?: string;
  assigned_project_id?: string;
  acknowledged_at?: string;
  resolution?: string;
  due_at?: string;
  caused_by?: string[];
  created_at: string;
  updated_at: string;
  etag: number;
  membership?: string;
}

/** Result of wrkq.task.findListView. */
export interface WrkqFindListView {
  items: WrkqFindEntry[];
  next_cursor?: string;
}

// ── Comments ─────────────────────────────────────────────────────────────────

export type WrkqCommentKind = "blocker" | "decision" | "postmortem" | "digest";

export interface WrkqCommentAddParams {
  task?: string;
  container?: string;
  kind?: WrkqCommentKind;
  body: string;
  meta?: Record<string, unknown>;
  idempotencyKey?: string;
}

export interface WrkqCommentListParams {
  task?: string;
  container?: string;
  includeDeleted?: boolean;
  limit?: number;
  cursor?: string;
}

export interface WrkqCommentShowParams {
  id: string;
}

export interface WrkqCommentDeleteParams {
  id: string;
}

export interface WrkqComment {
  uuid: string;
  id: string;
  task?: string;
  container?: string;
  kind?: WrkqCommentKind;
  body: string;
  meta: Record<string, unknown>;
  etag: number;
  createdAt: string;
  updatedAt?: string;
  deletedAt?: string;
  createdByPrincipalRef?: string;
}

export interface WrkqCommentListResult {
  items: WrkqComment[];
  nextCursor?: string;
}

// ── Attachments ──────────────────────────────────────────────────────────────

export interface WrkqAttachmentAddParams {
  task: string;
  path: string;
  filename?: string;
  mimeType?: string;
  idempotencyKey?: string;
}

export interface WrkqAttachmentListParams {
  task: string;
  limit?: number;
  cursor?: string;
}

export interface WrkqAttachmentShowParams {
  id: string;
}

export interface WrkqAttachmentRemoveParams {
  id: string;
}

export interface WrkqAttachment {
  uuid: string;
  id: string;
  taskUuid: string;
  filename: string;
  relativePath?: string;
  mimeType?: string;
  sizeBytes: number;
  /** Content checksum (DB/CLI field name; not "sha256"). */
  checksum?: string;
  createdAt: string;
  createdByPrincipalRef?: string;
}

export interface WrkqAttachmentListResult {
  items: WrkqAttachment[];
  nextCursor?: string;
}

// ── Relations ────────────────────────────────────────────────────────────────

export interface WrkqRelationAddParams {
  fromTask: string;
  kind: "blocks" | "relates_to" | "duplicates" | string;
  toTask: string;
  idempotencyKey?: string;
}

export interface WrkqRelationListParams {
  task: string;
}

export interface WrkqRelationRemoveParams {
  fromTask: string;
  kind: "blocks" | "relates_to" | "duplicates" | string;
  toTask: string;
}

export interface WrkqRelation {
  fromTask: string;
  toTask: string;
  kind: string;
  /** "outgoing" or "incoming" relative to the queried task. */
  direction?: string;
  createdAt?: string;
}

export interface WrkqRelationListResult {
  items: WrkqRelation[];
}

// ── Containers ───────────────────────────────────────────────────────────────

export interface WrkqContainerShowParams {
  path?: string;
  project?: string;
}

export interface WrkqContainerCreateParams {
  path?: string;
  project?: string;
  parentPath?: string;
  slug?: string;
  title?: string;
  kind?: string;
  actor?: string;
}

export interface WrkqContainerListParams {
  project?: string;
  includeArchived?: boolean;
  limit?: number;
  cursor?: string;
}

export interface WrkqContainer {
  uuid: string;
  id: string;
  slug: string;
  title: string;
  description: string;
  specification?: string;
  labels: string[];
  campaignState?: WrkqCampaignState;
  kind: string;
  parentUuid?: string;
  path: string;
  etag: number;
  createdAt: string;
  updatedAt: string;
  archivedAt?: string;
}

export type WrkqCampaignState = "draft" | "active" | "completed" | "cancelled";

export interface WrkqContainerListResult {
  items: WrkqContainer[];
  nextCursor?: string;
}

/**
 * Selects rows for wrkq.container.taskCounts. Counts always cover the complete
 * descendant subtree; this flag controls only whether archived containers have
 * their own result rows.
 */
export interface WrkqContainerTaskCountsParams {
  includeArchived?: boolean;
}

/** One stable container/project identity plus producer-owned subtree counts. */
export interface WrkqContainerTaskCount {
  uuid: string;
  id: string;
  path: string;
  kind: string;
  projectUuid?: string;
  projectId?: string;
  projectSlug?: string;
  archivedAt?: string;
  totalTaskCount: number;
  activeTaskCount: number;
}

/** Complete, stable-path-ordered, unpaginated container-count snapshot. */
export interface WrkqContainerTaskCounts {
  items: WrkqContainerTaskCount[];
}

// ── Project root registry ───────────────────────────────────────────────────

/**
 * One top-level project row from wrkq.project.listView. `root` is the stored
 * host-portable string verbatim; callers expand ~/... for the current host.
 */
export interface WrkqProjectEntry {
  type: "project";
  id: string;
  slug: string;
  title?: string;
  path: string;
  root: string | null;
}

export interface WrkqProjectListViewParams {
  includeArchived?: boolean;
  limit?: number;
  cursor?: string;
}

export interface WrkqProjectsListView {
  items: WrkqProjectEntry[];
  next_cursor?: string;
}

/**
 * Assign or clear a top-level project's registered checkout root. The RPC
 * stores `root` verbatim; CLI callers normalize paths beneath HOME to ~/....
 */
export interface WrkqProjectSetRootParams {
  project: string;
  /** Empty string clears the registry field. */
  root: string;
  /** Optional CAS; stale values fail without changing root, etag, attribution, or events. */
  expectEtag?: number;
  /** Canonical caller principal (`agent:<id>` or full agent ScopeRef); bare legacy identities are invalid. */
  actor?: string;
}

/**
 * Global webhook subscriptions live on the SINGLETON ROOT container and are
 * inherited by every project. This is a DEDICATED family (wrkq.webhook.add /
 * remove / listView, T-05119 daedalus #10211), deliberately separate from
 * wrkq.container.update (whose narrow patch surface rejects webhookUrls). Mirrors
 * docs/wrkq-wrkf-rpc.md §6.2 "Global webhook methods".
 */
export interface WrkqWebhookMutateParams {
  url: string;
  /** Optional root-container etag CAS; absent = legacy no-CAS. Stale → WRKQ_CONFLICT. */
  expectEtag?: number;
  actor?: string;
}

/**
 * One stored webhook_urls entry: a bare URL (every event family) or a URL
 * narrowed to event classes ("task" | "workflow" | "container", "*" / "all") or
 * exact event names. A list written as bare URLs stays bare on the wire.
 */
export type WrkqWebhookSubscription = string | { url: string; events?: string[] };

/**
 * The legacy MUTATION RESULT for wrkq.webhook.add / remove, in MAP-ALPHABETICAL
 * key order (this OVERRIDES the struct-field-order convention): a changed result
 * carries { changed, count, target, webhook_urls }; a no-change result carries
 * only { changed, webhook_urls }. count/target are present only when changed.
 * The global family is URL-only but PRESERVES structured entries written on the
 * root by `container set`, so webhook_urls may contain narrowed entries.
 */
export interface WrkqWebhookMutateResult {
  changed: boolean;
  count?: number;
  target?: string;
  webhook_urls: WrkqWebhookSubscription[];
}

export type WrkqWebhookListViewParams = Record<string, never>;

/** Legacy {url:<value>} row returned by wrkq.webhook.listView. */
export interface WrkqWebhookRow {
  url: string;
}

/**
 * WrkqContainerUpdateParams renames a container in place. The FIRST patch surface
 * is deliberately NARROW — only { slug?, title? }; any other key →
 * WRKQ_VALIDATION (T-05112 daedalus hrcchat#10196). Mirrors docs/wrkq-wrkf-rpc.md
 * §6.2 WrkqContainerUpdateParams. Returns the updated WrkqContainer.
 */
export interface WrkqContainerUpdateParams {
  container: string;
  patch: { slug?: string; title?: string };
  /** Optional etag CAS; stale → WRKQ_CONFLICT. */
  expectEtag?: number;
  actor?: string;
  idempotencyKey?: string;
}

export interface WrkqContainerCampaignConvertParams {
  container: string;
  state?: "draft" | "active";
  description?: string;
  specification?: string;
  labels?: string[];
  expectEtag?: number;
  actor?: string;
}

export interface WrkqContainerCampaignUpdateParams {
  container: string;
  description?: string;
  specification?: string;
  labels?: string[];
  expectEtag?: number;
  actor?: string;
}

export interface WrkqContainerCampaignActivateParams {
  container: string;
  expectEtag?: number;
  actor?: string;
}

export interface WrkqContainerCampaignCloseParams {
  container: string;
  state: "completed" | "cancelled";
  expectEtag?: number;
  actor?: string;
}

export interface WrkqCampaignMemberDiagnostic {
  uuid: string;
  id: string;
  path: string;
  state: WrkqTaskState;
  membership: "resident" | "enrolled";
}

export interface WrkqCampaignTransitionResult {
  container: WrkqContainer;
  previousState: WrkqCampaignState | null;
  campaignState: WrkqCampaignState;
  missingOutcomes: WrkqCampaignMemberDiagnostic[];
  eventId: number;
  eventTimestamp: string;
}

export interface WrkqContainerCampaignPortfolioParams {
  states?: WrkqCampaignState[];
  includeArchived?: boolean;
}

export interface WrkqCampaignProject {
  uuid: string;
  id: string;
  slug: string;
  title: string;
}

export interface WrkqCampaignFootprint {
  project: WrkqCampaignProject;
  memberCount: number;
}

export interface WrkqCampaignPortfolioRow {
  container: WrkqContainer;
  totalMembers: number;
  stateCounts: Record<string, number>;
  residentCount: number;
  enrolledCount: number;
  inProgressCount: number;
  missingOutcomeCount: number;
  footprint: WrkqCampaignFootprint[];
  lastActivityAt: string;
}

export interface WrkqCampaignPortfolio {
  items: WrkqCampaignPortfolioRow[];
}

export interface WrkqContainerTimelineViewParams {
  container: string;
  cursor?: string;
  limit?: number;
}

export interface WrkqTimelineContainer {
  uuid: string;
  id: string;
  slug: string;
  title: string;
  description: string;
  specification?: string;
  labels: string[];
  kind: string;
  parentUuid?: string;
  path: string;
  etag: number;
  createdAt: string;
  updatedAt: string;
  archivedAt?: string;
}

export interface WrkqCampaignAdornment {
  state: WrkqCampaignState;
  archived: boolean;
  archivedAt?: string;
}

export interface WrkqTimelineMember {
  uuid: string;
  id: string;
  path: string;
  title: string;
  state: WrkqTaskState;
  outcome?: string;
  membership: "resident" | "enrolled";
  project: WrkqCampaignProject;
}

export interface WrkqTimelineRollup {
  terminal: number;
  total: number;
}

export interface WrkqTimelineComment {
  id?: string;
  kind?: WrkqCommentKind;
  body: string;
  meta?: Record<string, unknown>;
}

export interface WrkqTimelineOutcome {
  text: string | null;
}

export interface WrkqTimelineTaskState {
  state: WrkqTaskState | "purged";
  sourceEventType:
    | "task.updated"
    | "task.archived"
    | "task.deleted"
    | "task.restored"
    | "task.purged";
}

export interface WrkqTimelineContainerState {
  from: WrkqCampaignState | null;
  to: WrkqCampaignState;
}

interface WrkqTimelineEntryBase {
  eventId: number;
  timestamp: string;
  principalRef?: string;
  resourceUuid?: string;
  taskUuid?: string;
  taskId?: string;
  taskPath?: string;
  membership?: "resident" | "enrolled";
  campaignUuid: string | null;
  containerUuid?: string;
}

export type WrkqTimelineEntry =
  | (WrkqTimelineEntryBase & {
      type: "comment";
      comment: WrkqTimelineComment;
    })
  | (WrkqTimelineEntryBase & {
      type: "task.outcome";
      outcome: WrkqTimelineOutcome;
    })
  | (WrkqTimelineEntryBase & {
      type: "task.state";
      taskState: WrkqTimelineTaskState;
    })
  | (WrkqTimelineEntryBase & {
      type: "container.state";
      containerState: WrkqTimelineContainerState;
    });

export interface WrkqContainerTimelineView {
  container: WrkqTimelineContainer;
  campaign: WrkqCampaignAdornment | null;
  members: WrkqTimelineMember[];
  rollup: WrkqTimelineRollup;
  missingOutcomes: WrkqCampaignMemberDiagnostic[];
  footprint: WrkqCampaignFootprint[];
  lastActivityAt: string;
  decisionTasks: WrkqTimelineMember[];
  entries: WrkqTimelineEntry[];
  snapshotEventId: number;
  nextCursor?: string;
}

export interface WrkqContainerDeleteParams {
  container?: string;
  path?: string;
  project?: string;
  expectEtag?: number;
  actor?: string;
}

export interface WrkqContainerDeleteResult {
  deleted: boolean;
}

export interface WrkqContainerDeleteRecursiveExpected {
  containers: number;
  tasks: number;
  attachments: number;
  bytes: number;
}

export interface WrkqContainerDeleteRecursiveParams {
  container?: string;
  path?: string;
  project?: string;
  dryRun?: boolean;
  expectEtag?: number;
  expected?: WrkqContainerDeleteRecursiveExpected;
  actor?: string;
}

export interface WrkqContainerDeleteRecursiveResult {
  container?: WrkqContainer;
  containers?: number;
  tasks?: number;
  attachments?: number;
  bytes?: number;
  deleted?: boolean;
  containersDeleted?: number;
  tasksDeleted?: number;
  attachmentsDeleted?: number;
  bytesFreed?: number;
  fileCleanupErrors?: string[];
}

// ── Task-workflow binding ────────────────────────────────────────────────────

export interface WrkqWorkflowAttachParams {
  task: string;
  /** Template ref, e.g. "code_change@1". */
  workflow: string;
  supersede?: boolean;
  predecessorInstanceId?: string;
  predecessorRevision?: number;
  attachDiscontinued?: boolean;
  actor?: string;
  idempotencyKey?: string;
}

export interface WrkqWorkflowAttachResult {
  task: WrkqTask;
  instance: WrkfInstance;
  /** true = newly attached, false = already existed. */
  attached: boolean;
}

export interface WrkqWorkflowInspectParams {
  task: string;
}

export interface WrkqWorkflowInspectResult {
  instance: WrkfInstance;
  [k: string]: unknown;
}

export interface WrkqWorkflowInstancesParams {
  task: string;
}

export interface WrkqWorkflowInstancesResult {
  instances: WrkfInstance[];
  [k: string]: unknown;
}

export interface WrkqWorkflowTimelineParams {
  task: string;
}

export interface WrkqWorkflowTimelineResult {
  events: WrkfEvent[];
  [k: string]: unknown;
}

export interface WrkqWorkflowRefreshParams {
  task: string;
  idempotencyKey?: string;
}

export interface WrkqWorkflowSyncMetaParams {
  task?: string;
  actor?: string;
}

export interface WrkqWorkflowSyncMetaResult {
  synced: number;
}

// ── Handoff (T-05117) ────────────────────────────────────────────────────────

/**
 * WrkqHandoff is the handoff resource DTO. Its fields are DELIBERATELY snake_case
 * (legacy `handoffJSON` byte-parity) — unlike the camelCase task/comment DTOs —
 * because the wrkq CLI re-marshals it verbatim. Field order matches the server
 * WrkqHandoff DTO (the pinned legacy fingerprint).
 */
export interface WrkqHandoff {
  uuid: string;
  id: string;
  scope_ref: string;
  scope_kind: string;
  agent_id: string;
  project_id: string;
  agent_actor_uuid: string | null;
  agent_principal_ref?: string;
  project_container_uuid: string | null;
  created_by_agent_id: string;
  created_by_actor_uuid: string | null;
  created_by_principal_ref?: string;
  title: string;
  body: string;
  status: "pending" | "acknowledged";
  idempotency_key: string | null;
  acknowledged_at: string | null;
  acknowledged_by_agent_id: string | null;
  acknowledged_by_actor_uuid: string | null;
  acknowledged_by_principal_ref?: string;
  acknowledgement_note: string | null;
  /** Raw JSON string of the meta object (legacy stores meta as a string), or null. */
  meta: string | null;
  etag: number;
  created_at: string;
  updated_at: string;
}

/**
 * Params for wrkq.handoff.create. Scope is CALLER-owned but NOT project-root: the
 * caller resolves the effective agent/project scope (and enforces self-scope) and
 * passes the EXPLICIT scopeRef/agentId/projectId + actor here. The server reads no
 * agent-runtime env. `meta` is the raw JSON-object STRING (legacy semantics).
 */
export interface WrkqHandoffCreateParams {
  scopeRef: string;
  agentId: string;
  projectId: string;
  title: string;
  body: string;
  /** Raw JSON-object string (legacy --meta). */
  meta?: string;
  idempotencyKey?: string;
  /** Caller-resolved create attribution; defaults to the scope agent. */
  actorAgentId?: string;
  principalRef?: string;
  /** Project the prospective handoff without writing. */
  dryRun?: boolean;
}

/** Result of wrkq.handoff.create: the handoff + whether it was an idempotent replay. */
export interface WrkqHandoffCreateResult {
  handoff: WrkqHandoff;
  idempotentReplay: boolean;
}

/** Params for wrkq.handoff.get. */
export interface WrkqHandoffGetParams {
  /** Friendly handoff ID (H-00001) or UUID. */
  handoff: string;
}

/**
 * Params for wrkq.handoff.listView. scopeRef is the CALLER-resolved canonical
 * project scope; the server never derives it from env.
 */
export interface WrkqHandoffListViewParams {
  scopeRef: string;
  /** pending (default) | acknowledged | all. */
  status?: "pending" | "acknowledged" | "all";
  limit?: number;
  cursor?: string;
}

/** Result of wrkq.handoff.listView: a caller-scoped page of handoffs. */
export interface WrkqHandoffListResult {
  items: WrkqHandoff[];
  nextCursor?: string;
}

/**
 * Params for wrkq.handoff.acknowledge. The caller passes the resolved acting
 * identity; the server owns the etag CAS + the handoff.acknowledged event.
 */
export interface WrkqHandoffAcknowledgeParams {
  handoff: string;
  note?: string;
  actorAgentId: string;
  principalRef?: string;
  scopeRef?: string;
  dryRun?: boolean;
  /** Reject when the current etag != this value (server CAS). */
  ifMatch?: number;
}
// ── search + index family (T-05114) ──────────────────────────────────────────
// The SERVER owns the derived <db>.search.sqlite sidecar + dense embedder behind
// wrkq.search.listView / wrkq.index.*. The client owns ONLY project-root path
// scoping (paths are pre-scoped before the call) + presentation. The result DTOs
// keep the LEGACY snake_case output keys (search.Response / search.Result /
// indexdb.Status field shapes) so machine consumers match `wrkq search --json`
// and `wrkq index status --json` byte-for-byte.

/** Params for wrkq.search.listView. Paths are already project-root scoped. */
export interface WrkqSearchListViewParams {
  query: string;
  /** Pre-scoped path prefixes to restrict results to. */
  paths?: string[];
  /** State filter ("" → open only; "all" → non-deleted). */
  state?: string;
  kind?: string;
  /** Exact, case-sensitive canonical task labels; every value must be present. */
  labels?: string[];
  /** Assignee principal ref (or bare agent slug the caller resolved). */
  assigneePrincipalRef?: string;
  limit?: number;
  candidateLimit?: number;
  /** "relevance" (default) | "updated_at" | "created_at". */
  sort?: string;
  reverse?: boolean;
  /** Fail (WRKQ_VALIDATION) if the index is stale rather than returning stale rows. */
  fresh?: boolean;
  /** Include ranking diagnostics under each result's `explain`. */
  explain?: boolean;
}

/** One search hit — LEGACY search.Result snake_case shape (struct order). */
export interface WrkqSearchResult {
  resource_type: string;
  resource_id: string;
  resource_uuid: string;
  task_id?: string;
  task_uuid?: string;
  comment_id?: string;
  scope_ref?: string;
  status?: string;
  path: string;
  title: string;
  state?: string;
  kind?: string;
  snippet: string;
  score: number;
  created_at: string;
  updated_at: string;
  stale: boolean;
  explain?: Record<string, unknown>;
}

/** Search index status — LEGACY indexdb.Status snake_case shape (struct order). */
export interface WrkqIndexStatus {
  path: string;
  enabled: boolean;
  status: string;
  last_indexed_event_id: number;
  canonical_max_event_id: number;
  stale_event_count: number;
  dense_model_id?: string;
  dense_dimension?: number;
  dense_vector_count?: number;
  last_error?: string;
  searchable_chunk_count: number;
}

/** Result of wrkq.search.listView — LEGACY search.Response snake_case shape. */
export interface WrkqSearchListView {
  query: string;
  stale: boolean;
  status: WrkqIndexStatus | null;
  results: WrkqSearchResult[];
  total_matches: number;
  offset: number;
}

/** Params for the index lifecycle methods (update/rebuild/vacuum/pause/resume). */
export interface WrkqIndexLifecycleParams {
  /** Accepted for `rebuild` surface parity; the server always runs synchronously. */
  foreground?: boolean;
}

/** Result of wrkq.index.update (legacy map-alphabetical ack). */
export interface WrkqIndexUpdateResult {
  status: WrkqIndexStatus;
  updated: boolean;
}

/** Result of wrkq.index.rebuild (legacy map-alphabetical ack). */
export interface WrkqIndexRebuildResult {
  rebuilt: boolean;
  status: WrkqIndexStatus;
}

/** Result of wrkq.index.vacuum (legacy map-alphabetical ack). */
export interface WrkqIndexVacuumResult {
  vacuumed: boolean;
}

/** Result of wrkq.index.pause / wrkq.index.resume (legacy map-alphabetical ack). */
export interface WrkqIndexStateResult {
  status: string;
}
