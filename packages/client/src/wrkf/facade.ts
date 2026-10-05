/**
 * wrkf/facade.ts — typed surface for the wrkf namespace (workflow behavior).
 *
 * This is a pure type. The concrete implementation lives in the root client
 * factory (src/client.ts). Subpath export `@wrkq/client/wrkf` re-exports this
 * type plus the wrkf DTOs.
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3. There is no task-mutation surface here:
 * `wrkf.*` may reference a `task` selector only to resolve the attached
 * instance.
 */

import type {
  WrkfActionBindExternalParams,
  WrkfActionClaimParams,
  WrkfActionClaimResult,
  WrkfActionCompleteParams,
  WrkfActionCompleteResult,
  WrkfActionFailParams,
  WrkfActionHeartbeatParams,
  WrkfActionListParams,
  WrkfActionListResult,
  WrkfActionNextParams,
  WrkfActionNextResult,
  WrkfActionRun,
  WrkfActionSettleParams,
  WrkfActionSettleResult,
  WrkfActionShowParams,
  WrkfActionStartParams,
} from "./action.js";
import type {
  WrkfCheckListParams,
  WrkfCheckPreflightParams,
  WrkfCheckRun,
  WrkfCheckRunParams,
  WrkfCheckRunResult,
  WrkfCheckShowParams,
  WrkfHookListParams,
  WrkfHookRunParams,
  WrkfHookShowParams,
} from "./check.js";
import type {
  WrkfEffect,
  WrkfEffectAckParams,
  WrkfEffectClaimParams,
  WrkfEffectClaimResult,
  WrkfEffectDeliverParams,
  WrkfEffectFailParams,
  WrkfEffectListParams,
  WrkfEffectRetryParams,
  WrkfEffectShowParams,
  WrkfSupervisorParams,
} from "./effect.js";
import type {
  WrkfEventQueryParams,
  WrkfEventQueryResult,
  WrkfWatchEventsParams,
  WrkfWatchEventsResult,
  WrkfWatchSnapshot,
  WrkfWatchSnapshotParams,
} from "./event.js";
import type {
  WrkfEvidence,
  WrkfEvidenceAddParams,
  WrkfEvidenceListParams,
  WrkfEvidenceSchema,
  WrkfEvidenceSchemaParams,
  WrkfEvidenceShowParams,
  WrkfEvidenceSuggestParams,
  WrkfSuggestResult,
} from "./evidence.js";
import type {
  WrkfInstance,
  WrkfInstanceCancelParams,
  WrkfInstanceCancelResult,
  WrkfInstanceNextParams,
  WrkfInstanceShowParams,
  WrkfNextResult,
  WrkfSuspensionResolveParams,
  WrkfSuspensionResolveResult,
} from "./instance.js";
import type {
  LedgerAppendInput,
  LedgerEntry,
  LedgerListFilter,
  LedgerListResult,
} from "./ledger.js";
import type {
  WrkfObligation,
  WrkfObligationCancelParams,
  WrkfObligationCreateParams,
  WrkfObligationListParams,
  WrkfObligationSatisfyParams,
  WrkfObligationShowParams,
  WrkfObligationWaiveParams,
} from "./obligation.js";
import type {
  WrkfRoleBindParams,
  WrkfRoleBinding,
  WrkfRoleListParams,
  WrkfRoleSetParams,
  WrkfRoleUnbindParams,
} from "./role.js";
import type {
  WrkfRun,
  WrkfRunBindExternalParams,
  WrkfRunFailParams,
  WrkfRunFinishParams,
  WrkfRunListParams,
  WrkfRunShowParams,
  WrkfRunStartParams,
} from "./run.js";
import type {
  WrkfDiffResult,
  WrkfInstallResult,
  WrkfWorkflowDiffParams,
  WrkfWorkflowInstallParams,
  WrkfWorkflowLifecycleParams,
  WrkfWorkflowListParams,
  WrkfWorkflowListResult,
  WrkfWorkflowShowParams,
  WrkfWorkflowShowResult,
  WrkfWorkflowValidateParams,
  WrkfWorkflowValidateResult,
} from "./template.js";
import type { WrkfTransitionApplyParams, WrkfTransitionResult } from "./transition.js";

export interface WrkfWorkflowFacade {
  validate(params: WrkfWorkflowValidateParams): Promise<WrkfWorkflowValidateResult>;
  show(params: WrkfWorkflowShowParams): Promise<WrkfWorkflowShowResult>;
  list(params?: WrkfWorkflowListParams): Promise<WrkfWorkflowListResult>;
  diff(params: WrkfWorkflowDiffParams): Promise<WrkfDiffResult>;
  install(params: WrkfWorkflowInstallParams): Promise<WrkfInstallResult>;
  discontinue(params: WrkfWorkflowLifecycleParams): Promise<WrkfWorkflowShowResult>;
  reinstate(params: WrkfWorkflowLifecycleParams): Promise<WrkfWorkflowShowResult>;
}

export interface WrkfInstanceFacade {
  show(params: WrkfInstanceShowParams): Promise<WrkfInstance>;
  next(params: WrkfInstanceNextParams): Promise<WrkfNextResult>;
  cancel(params: WrkfInstanceCancelParams): Promise<WrkfInstanceCancelResult>;
}

export interface WrkfEvidenceFacade {
  add(params: WrkfEvidenceAddParams): Promise<WrkfEvidence>;
  list(params: WrkfEvidenceListParams): Promise<WrkfEvidence[]>;
  show(params: WrkfEvidenceShowParams): Promise<WrkfEvidence>;
  suggest(params: WrkfEvidenceSuggestParams): Promise<WrkfSuggestResult>;
  schema(params: WrkfEvidenceSchemaParams): Promise<WrkfEvidenceSchema>;
}

export interface WrkfLedgerFacade {
  append(input: LedgerAppendInput): Promise<LedgerEntry>;
  list(filter: LedgerListFilter): Promise<LedgerListResult>;
}

export interface WrkfRoleFacade {
  list(params: WrkfRoleListParams): Promise<WrkfRoleBinding[]>;
  bind(params: WrkfRoleBindParams): Promise<WrkfRoleBinding>;
  unbind(params: WrkfRoleUnbindParams): Promise<WrkfRoleBinding[]>;
  set(params: WrkfRoleSetParams): Promise<WrkfRoleBinding[]>;
}

export interface WrkfEventFacade {
  query(params?: WrkfEventQueryParams): Promise<WrkfEventQueryResult>;
}

export interface WrkfObligationFacade {
  list(params: WrkfObligationListParams): Promise<WrkfObligation[]>;
  show(params: WrkfObligationShowParams): Promise<WrkfObligation>;
  satisfy(params: WrkfObligationSatisfyParams): Promise<WrkfObligation>;
  waive(params: WrkfObligationWaiveParams): Promise<WrkfObligation>;
  cancel(params: WrkfObligationCancelParams): Promise<WrkfObligation>;
  create(params: WrkfObligationCreateParams): Promise<WrkfObligation>;
}

export interface WrkfSupervisorFacade {
  call(params: WrkfSupervisorParams): Promise<WrkfEffect>;
  escalate(params: WrkfSupervisorParams): Promise<WrkfEffect>;
}

export interface WrkfWatchFacade {
  snapshot(params: WrkfWatchSnapshotParams): Promise<WrkfWatchSnapshot>;
  events(params: WrkfWatchEventsParams): Promise<WrkfWatchEventsResult>;
}

export interface WrkfCheckFacade {
  preflight(params: WrkfCheckPreflightParams): Promise<unknown>;
  run(params: WrkfCheckRunParams): Promise<WrkfCheckRunResult>;
  show(params: WrkfCheckShowParams): Promise<WrkfCheckRun>;
  list(params: WrkfCheckListParams): Promise<unknown[]>;
}

export interface WrkfHookFacade {
  list(params?: WrkfHookListParams): Promise<unknown>;
  show(params: WrkfHookShowParams): Promise<unknown>;
  run(params: WrkfHookRunParams): Promise<unknown>;
}

export interface WrkfTransitionFacade {
  apply(params: WrkfTransitionApplyParams): Promise<WrkfTransitionResult>;
}

export interface WrkfSuspensionFacade {
  /** Atomically resolve an instance's active suspension by its id (the only gate). */
  resolve(params: WrkfSuspensionResolveParams): Promise<WrkfSuspensionResolveResult>;
}

export interface WrkfRunFacade {
  start(params: WrkfRunStartParams): Promise<WrkfRun>;
  bindExternal(params: WrkfRunBindExternalParams): Promise<WrkfRun>;
  finish(params: WrkfRunFinishParams): Promise<WrkfRun>;
  fail(params: WrkfRunFailParams): Promise<WrkfRun>;
  show(params: WrkfRunShowParams): Promise<WrkfRun>;
  list(params: WrkfRunListParams): Promise<WrkfRun[]>;
}

export interface WrkfActionFacade {
  next(params: WrkfActionNextParams): Promise<WrkfActionNextResult>;
  /** Claim with an explicit predecessor CAS acknowledgment; refuses with WRKF_SUSPENDED while parked. */
  claim(params: WrkfActionClaimParams): Promise<WrkfActionClaimResult>;
  settle(params: WrkfActionSettleParams): Promise<WrkfActionSettleResult>;
  start(params: WrkfActionStartParams): Promise<WrkfActionRun>;
  bindExternal(params: WrkfActionBindExternalParams): Promise<WrkfActionRun>;
  complete(params: WrkfActionCompleteParams): Promise<WrkfActionCompleteResult>;
  fail(params: WrkfActionFailParams): Promise<WrkfActionRun>;
  heartbeat(params: WrkfActionHeartbeatParams): Promise<WrkfActionRun>;
  renewLease(params: WrkfActionHeartbeatParams): Promise<WrkfActionRun>;
  show(params: WrkfActionShowParams): Promise<WrkfActionRun>;
  list(params: WrkfActionListParams): Promise<WrkfActionListResult>;
}

export interface WrkfEffectFacade {
  list(params: WrkfEffectListParams): Promise<WrkfEffect[]>;
  show(params: WrkfEffectShowParams): Promise<WrkfEffect>;
  claim(params: WrkfEffectClaimParams): Promise<WrkfEffectClaimResult>;
  ack(params: WrkfEffectAckParams): Promise<WrkfEffect>;
  fail(params: WrkfEffectFailParams): Promise<WrkfEffect>;
  retry(params: WrkfEffectRetryParams): Promise<WrkfEffect>;
  deliver(params: WrkfEffectDeliverParams): Promise<unknown>;
}

export interface WrkfFacade {
  readonly workflow: WrkfWorkflowFacade;
  readonly instance: WrkfInstanceFacade;
  readonly evidence: WrkfEvidenceFacade;
  readonly ledger: WrkfLedgerFacade;
  readonly event: WrkfEventFacade;
  readonly role: WrkfRoleFacade;
  readonly obligation: WrkfObligationFacade;
  readonly supervisor: WrkfSupervisorFacade;
  readonly watch: WrkfWatchFacade;
  readonly check: WrkfCheckFacade;
  readonly hook: WrkfHookFacade;
  readonly transition: WrkfTransitionFacade;
  readonly suspension: WrkfSuspensionFacade;
  readonly run: WrkfRunFacade;
  readonly action: WrkfActionFacade;
  readonly effect: WrkfEffectFacade;
}
