/**
 * wrkf/template.ts — workflow template registry (wrkf.workflow.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

export interface WrkfWorkflowTemplateSummary {
  id: string;
  version: string;
  hash: string;
  kind?: string;
  description?: string;
  installedAt?: string;
  installedBy?: string;
  discontinuedAt?: string;
  discontinuedBy?: string;
  [k: string]: unknown;
}

export interface WrkfWorkflowValidateParams {
  body: string;
  sourceName?: string;
}

export interface WrkfWorkflowValidateResult {
  valid: boolean;
  [k: string]: unknown;
}

export interface WrkfWorkflowShowParams {
  ref: string;
}

export interface WrkfWorkflowShowResult {
  template: Record<string, unknown>;
  hash: string;
  discontinuedAt?: string;
  discontinuedBy?: string;
}

export interface WrkfWorkflowListParams {
  [k: string]: unknown;
}

export interface WrkfWorkflowListResult {
  templates: WrkfWorkflowTemplateSummary[];
}

export interface WrkfWorkflowDiffParams {
  oldBody: string;
  newBody: string;
  oldSourceName?: string;
  newSourceName?: string;
}

export interface WrkfDiffResult {
  old: WrkfWorkflowTemplateSummary;
  new: WrkfWorkflowTemplateSummary;
  sameHash: boolean;
  [k: string]: unknown;
}

export interface WrkfWorkflowInstallParams {
  body: string;
  sourceName?: string;
}

export interface WrkfWorkflowLifecycleParams {
  ref: string;
  principal_ref?: string;
}

export interface WrkfInstallResult {
  id: string;
  version: string;
  hash: string;
  installed: boolean;
}
