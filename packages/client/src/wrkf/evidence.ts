/**
 * wrkf/evidence.ts — evidence records, schemas and suggestions (wrkf.evidence.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

export interface WrkfEvidenceBuild {
  id?: string;
  version?: string;
  env?: string;
}

export interface WrkfEvidence {
  id: string;
  instanceId?: string;
  kind?: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  data?: unknown;
  principal_ref?: string;
  role?: string;
  /** Persisted run linkage; see docs/wrkq-wrkf-rpc.md §9.7. */
  runId?: string;
  contentHash?: string;
  build?: WrkfEvidenceBuild;
  producedAt?: string;
  [k: string]: unknown;
}

export interface WrkfEvidenceSchema {
  kind: string;
  description?: string;
  class?: string;
  facts?: Record<string, unknown>;
  producibleBy?: string[];
  linkageRefs?: Array<Record<string, unknown>>;
}

export interface WrkfEvidenceAddParams {
  task?: string;
  instanceId?: string;
  kind: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  data?: unknown;
  principal_ref?: string;
  role?: string;
  /** Persisted run linkage; see docs/wrkq-wrkf-rpc.md §9.7. */
  runId?: string;
  contentHash?: string;
  build?: WrkfEvidenceBuild;
  idempotencyKey?: string;
}

export interface WrkfEvidenceListParams {
  task?: string;
  instanceId?: string;
}

export interface WrkfEvidenceShowParams {
  id: string;
}

export interface WrkfEvidenceSuggestParams {
  task?: string;
  instanceId?: string;
  transition: string;
}

export interface WrkfEvidenceSchemaParams {
  task: string;
  kind: string;
}

export interface WrkfSuggestResult {
  transition: string;
  required: unknown[];
  missing: string[];
  checks: string[];
  warnings: string[];
  [k: string]: unknown;
}
