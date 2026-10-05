/**
 * wrkq/task.ts — task DTOs: create/show/list/update, claims, move, restore,
 * copy, and the find compatibility projection.
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

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

export interface WrkqTaskCreateParams {
  subtaskOwner?: string;
  slug?: string;
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
  /** Requester principal (agent:<id> or bare slug). */
  requesterPrincipalRef?: string | null;
  /** Requester scope: canonical ScopeRef or <id>@<project>[:<lane>]; its agent must match the principal. */
  requesterScopeRef?: string | null;
  labels?: string[];
  meta?: Record<string, unknown>;
  riskClass?: WrkqRiskClass;
  /** Campaign ID/path to enroll the new task in; the task keeps its own project. */
  campaign?: string;
  principalRef?: string;
  idempotencyKey?: string;
}

export interface WrkqTaskShowParams {
  task: string;
}

export interface WrkqTaskListParams {
  subtasks?: boolean;
  subtaskOwner?: string;
  ownerState?: WrkqTaskState | "terminal";
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
  /** Normalized writer principal; scopeRef, when present, must name this agent. */
  actor?: string;
  /** Canonical full writer seat ScopeRef. */
  scopeRef?: string;
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
    /** "" clears both requester fields. */
    requesterPrincipalRef?: string | null;
    /** "" clears only the scope. A scope alone derives the principal. */
    requesterScopeRef?: string | null;
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

export interface WrkqSubtaskSummary {
  id: string;
  slug: string;
  title: string;
  state: WrkqTaskState;
  claimedBy?: string;
}

export interface WrkqTask {
  roomLocator?: string;
  subtaskOwner?: string;
  subtaskOwnerUuid?: string;
  openSubtaskCount: number;
  subtasks?: WrkqSubtaskSummary[];
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
  /** Who asked for the work; distinct from creator attribution and requestedBy. */
  requesterPrincipalRef?: string;
  requesterScopeRef?: string;
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
  subtasks?: boolean;
  ownerState?: WrkqTaskState | "terminal";
  paths?: string[];
  type?: "t" | "p";
  slugGlob?: string;
  /**
   * One exact state, or "all". Omitted: the producer-owned actionable set
   * (draft, open, in_progress, blocked), except that `ackPending` alone
   * selects unacknowledged completed/cancelled tasks.
   */
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
  subtask_owner_id?: string;
  open_subtask_count: number;
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
  requester_principal_ref?: string;
  requester_scope_ref?: string;
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
