/**
 * wrkq/promise.ts — promise DTOs (wrkq.promise.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

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
