/**
 * wrkf/ledger.ts — immutable instance ledger (wrkf.ledger.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

/** Platform lifecycle kinds are documented, while workflow-owned kinds remain open. */
export type LedgerKind =
  | "runner_start"
  | "runner_exit"
  | "park"
  | "resume"
  | "operator_intervention"
  | "escalation"
  | "strike"
  | "close_scorecard"
  | (string & {});

/** Platform artifact vocabulary carried inside a ledger body's free-form refs. */
export interface LedgerRefs {
  comments?: string[];
  dms?: string[];
  evidence?: string[];
  logs?: Record<string, string>;
  pids?: string[];
  runtime?: string[];
  trace_id?: string;
  artifacts_dir?: string;
  template?: string;
}

export interface LedgerEntry {
  seq: number;
  uuid: string;
  instanceId: string;
  taskId: string;
  ts: string;
  kind: LedgerKind;
  aboutPrincipalRef: string;
  writtenBy: string;
  body: Record<string, unknown> & { refs?: LedgerRefs };
}

export interface LedgerAppendInput {
  taskId: string;
  kind: LedgerKind;
  aboutPrincipalRef: string;
  body?: Record<string, unknown> & { refs?: LedgerRefs };
}

export interface LedgerListFilter {
  taskId?: string;
  aboutPrincipalRef?: string;
  kind?: LedgerKind;
  since?: string;
  until?: string;
  limit?: number;
  cursor?: string;
}

export interface LedgerListResult {
  entries: LedgerEntry[];
  nextCursor?: string;
}
