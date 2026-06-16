/**
 * wrkq/types.ts — DTOs for the wrkq namespace (tasks, comments, attachments,
 * relations, containers, task-workflow binding).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7. All RPC DTO JSON fields are
 * camelCase. Field sets verified against the live `wrkq rpc --stdio` server
 * (proto 2026-06-14).
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
}

export interface WrkqTaskUpdateParams {
  task: string;
  patch: {
    title?: string;
    description?: string;
    specification?: string;
    state?: WrkqTaskState;
    priority?: number;
    kind?: string;
    labels?: string[];
    meta?: Record<string, unknown>;
    riskClass?: WrkqRiskClass;
    assigneePrincipalRef?: string | null;
    dueAt?: string | null;
    startAt?: string | null;
  };
  /** CAS precondition; see docs/wrkq-wrkf-rpc.md §8.1. */
  expectEtag?: number;
  idempotencyKey?: string;
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

export interface WrkqTaskRestoreParams {
  task: string;
  /** Target state (default "open"); archived/deleted targets are rejected. */
  state?: string;
}

export interface WrkqTask {
  uuid: string;
  id: string;
  slug: string;
  title: string;
  projectUuid: string;
  path: string;
  state: WrkqTaskState;
  priority: number;
  kind: string;
  description: string;
  specification: string;
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
  createdByPrincipalRef?: string;
  updatedByPrincipalRef?: string;
}

export interface WrkqTaskListResult {
  items: WrkqTask[];
  nextCursor?: string;
}

// ── Comments ─────────────────────────────────────────────────────────────────

export interface WrkqCommentAddParams {
  task: string;
  body: string;
  meta?: Record<string, unknown>;
  idempotencyKey?: string;
}

export interface WrkqCommentListParams {
  task: string;
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
  task: string;
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
  kind: string;
  parentUuid?: string;
  path: string;
  etag: number;
  createdAt: string;
  updatedAt: string;
  archivedAt?: string;
}

export interface WrkqContainerListResult {
  items: WrkqContainer[];
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

// ── Legacy actor admin cache (wrkq.admin.legacyActor.*) ──────────────────────

/**
 * WrkqLegacyActor is the legacy actor display/admin-cache DTO (T-04850).
 *
 * Actor rows are a LEGACY read/admin compatibility surface: they do NOT issue,
 * validate, or authorize principals. There is deliberately no `principalRef`
 * field. Task/comment/container/workflow writes attribute by `principalRef` and
 * never require an actor row. Prefer principal-based attribution for new work.
 */
export interface WrkqLegacyActor {
  uuid: string;
  id: string;
  slug: string;
  displayName?: string;
  /** One of "human" | "agent" | "system". */
  role: string;
  meta?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}

/** Result envelope for wrkq.admin.legacyActor.list. */
export interface WrkqLegacyActorListResult {
  items: WrkqLegacyActor[];
}

/** Params for wrkq.admin.legacyActor.list (no filters today). */
export interface WrkqLegacyActorListParams {
  [k: string]: never;
}

/** Params for wrkq.admin.legacyActor.create. */
export interface WrkqLegacyActorCreateParams {
  slug: string;
  displayName?: string;
  /** Defaults to "agent". One of "human" | "agent" | "system". */
  role?: string;
  /** Replacement meta object (must be a JSON object). */
  meta?: Record<string, unknown>;
  idempotencyKey?: string;
}

/** Mutable fields for wrkq.admin.legacyActor.update. slug/id/uuid/createdAt are immutable. */
export interface WrkqLegacyActorPatch {
  /** Set or clear (null) the display name. */
  displayName?: string | null;
  /** One of "human" | "agent" | "system". */
  role?: string;
  /** Replace (object) or clear (null) the meta object. */
  meta?: Record<string, unknown> | null;
}

/** Params for wrkq.admin.legacyActor.update. */
export interface WrkqLegacyActorUpdateParams {
  /** Actor selector: uuid, friendly id, or slug. */
  actor: string;
  patch: WrkqLegacyActorPatch;
  /** Optimistic-concurrency guard against the actor's current updatedAt. */
  expectUpdatedAt?: string;
  idempotencyKey?: string;
}
