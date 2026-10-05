/**
 * wrkq/comment.ts — comment DTOs (wrkq.comment.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

export type WrkqCommentKind = "blocker" | "decision" | "postmortem" | "digest";

export interface WrkqSessionRef {
  hostSessionId: string;
  generation: number;
}

export interface WrkqCommentAddParams {
  task?: string;
  container?: string;
  actor?: string;
  /** Canonical full writer seat ScopeRef. */
  scopeRef?: string;
  session?: WrkqSessionRef;
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
  created_by_host_session_id?: string;
  created_by_generation?: number;
}

export interface WrkqCommentListResult {
  items: WrkqComment[];
  nextCursor?: string;
}
