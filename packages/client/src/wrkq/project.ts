/**
 * wrkq/project.ts — project root registry DTOs (wrkq.project.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

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
