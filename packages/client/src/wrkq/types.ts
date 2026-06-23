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
  /** Omit description/specification bodies from list items while retaining presence booleans. */
  summary?: boolean;
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
  path: string;
  state: WrkqTaskState;
  priority: number;
  kind: string;
  description: string;
  specification: string;
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
 * The legacy MUTATION RESULT for wrkq.webhook.add / remove, in MAP-ALPHABETICAL
 * key order (this OVERRIDES the struct-field-order convention): a changed result
 * carries { changed, count, target, webhook_urls }; a no-change result carries
 * only { changed, webhook_urls }. count/target are present only when changed.
 */
export interface WrkqWebhookMutateResult {
  changed: boolean;
  count?: number;
  target?: string;
  webhook_urls: string[];
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

// ── Bundle logical snapshot (wrkq.bundle.exportView) ─────────────────────────

/**
 * WrkqBundleExportViewParams selects the bundle filter/scope set. The caller
 * applies project-root scoping to `project`/`pathPrefixes` BEFORE calling; the
 * server never reads project-root env/flags. At least one of
 * actor/since/until/project/pathPrefixes is required (else WRKQ_VALIDATION).
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 (T-05118, daedalus hrcchat#10211).
 */
export interface WrkqBundleExportViewParams {
  actor?: string;
  /** Cursor (event:<id> or ts:<rfc3339>) or RFC3339 timestamp. */
  since?: string;
  /** End timestamp (RFC3339). */
  until?: string;
  /** Project selector (path/uuid), already project-root scoped. */
  project?: string;
  /** Path prefixes (already project-root scoped). */
  pathPrefixes?: string[];
  includeRefs?: boolean;
  /**
   * Attachment bytes do NOT cross in the snapshot (descriptors only). The CLI
   * mirror HARD-GATES --with-attachments; raw clients should leave this false.
   */
  withAttachments?: boolean;
  withEvents?: boolean;
  /** Build identity stamped verbatim into the manifest. */
  version?: string;
  commit?: string;
  buildDate?: string;
}

/**
 * WrkqBundleManifest is the manifest.json wire shape. Field NAMES are
 * snake_case (the legacy manifest JSON keys); the on-disk field ORDER is the
 * server contract (NOT alphabetical), preserved when the CLI materializes.
 */
export interface WrkqBundleManifest {
  machine_interface_version: number;
  version?: string;
  commit?: string;
  build_date?: string;
  timestamp: string;
  actor?: string;
  since?: string;
  until?: string;
  since_cursor?: string;
  project?: string;
  project_uuid?: string;
  path_prefixes?: string[];
  with_attachments: boolean;
  with_events: boolean;
  include_refs?: boolean;
  ref_count?: number;
}

/**
 * WrkqBundleTaskDoc is one task/ref document: bundle `path` (no .md suffix),
 * base_etag, uuid, and the full rendered markdown `content` written verbatim to
 * tasks/<path>.md or refs/<path>.md when the snapshot is materialized.
 */
export interface WrkqBundleTaskDoc {
  path: string;
  base_etag?: number;
  uuid?: string;
  content: string;
}

/** One exported event row (events.ndjson row shape + order). */
export interface WrkqBundleEventRow {
  id: number;
  timestamp: string;
  actor_uuid: string | null;
  resource_type: string;
  resource_uuid: string;
  event_type: string;
  etag: number | null;
  payload?: string;
}

/** One attachment descriptor (NO bytes — descriptors only). */
export interface WrkqBundleAttachmentDescriptor {
  task_uuid: string;
  filename: string;
}

/**
 * WrkqBundleExportView is the server-owned LOGICAL bundle snapshot read under one
 * transaction. It carries NOTHING about the caller-host filesystem; the CLI
 * materializes the directory from it. snake_case keys inside the doc/manifest/
 * event/descriptor shapes are deliberate (legacy on-disk byte parity).
 */
export interface WrkqBundleExportView {
  manifest: WrkqBundleManifest;
  tasks: WrkqBundleTaskDoc[];
  containers: string[];
  refs?: WrkqBundleTaskDoc[];
  events?: WrkqBundleEventRow[];
  attachments?: WrkqBundleAttachmentDescriptor[];
}
