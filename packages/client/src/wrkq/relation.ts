/**
 * wrkq/relation.ts — task relation DTOs (wrkq.relation.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

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
