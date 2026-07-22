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
