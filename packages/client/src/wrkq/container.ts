/**
 * wrkq/container.ts — container DTOs: show/create/list/update/delete and
 * subtree task counts.
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

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
