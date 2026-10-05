/**
 * wrkf/check.ts — check runs and catalog hooks (wrkf.check.*, wrkf.hook.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

export interface WrkfCheckRun {
  id: string;
  verdict?: string;
  outcome?: string;
  [k: string]: unknown;
}

export interface WrkfCheckPreflightParams {
  task?: string;
  instanceId?: string;
  transition: string;
}

export interface WrkfCheckRunParams {
  task?: string;
  instanceId?: string;
  transition: string;
}

export interface WrkfCheckRunResult {
  runs: WrkfCheckRun[];
  [k: string]: unknown;
}

export interface WrkfCheckShowParams {
  id: string;
}

export interface WrkfCheckListParams {
  task?: string;
  instanceId?: string;
  transition?: string;
}

export interface WrkfHookListParams {
  [k: string]: unknown;
}

export interface WrkfHookShowParams {
  id: string;
}

export interface WrkfHookRunParams {
  task?: string;
  instanceId?: string;
  transition: string;
  hookId: string;
}
