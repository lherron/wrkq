/**
 * wrkq/handoff.ts — handoff DTOs (wrkq.handoff.*, T-05117).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

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
