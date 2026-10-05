/**
 * wrkq/project-event.ts — foreign project fact DTOs (wrkq.projectEvent.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

export interface WrkqProjectEventPostParams {
  project?: string;
  task?: string;
  type: string;
  summary: string;
  attributes: Record<string, string>;
  idempotencyKey?: string;
  occurredAt?: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqProjectEventGetParams {
  projectEvent: string;
}
export interface WrkqProjectEventTypesViewParams {
  project?: string;
}

export interface WrkqProjectEvent {
  uuid: string;
  projectUuid: string;
  containerUuid: string;
  campaignUuid: string | null;
  taskUuid: string | null;
  type: string;
  attributes: Record<string, string>;
  principalRef: string | null;
  scopeRef: string | null;
  summary: string;
  idempotencyKey: string | null;
  occurredAt: string;
  createdAt: string;
  task: string | null;
  container: string | null;
  campaign: string | null;
}

export interface WrkqProjectEventPostResult {
  uuid: string;
  created: boolean;
}

export interface WrkqProjectEventType {
  type: string;
  count: number;
  lastCreatedAt: string;
}

export interface WrkqProjectEventTypesView {
  items: WrkqProjectEventType[];
}
