/**
 * @wrkq/client/wrkf — wrkf namespace types + facade type only.
 *
 * No concrete client lives here; construct it via `createClient` from the root
 * package (spec §7.6).
 *
 * Several wrkf result envelopes return broad workflow-runtime structs; the DTO
 * modules type the well-known fields and allow extra keys (`[k: string]: unknown`)
 * rather than pin a field that may not exist for every template/state.
 */

export type * from "./action.js";
export type * from "./check.js";
export type * from "./effect.js";
export type * from "./event.js";
export type * from "./evidence.js";
export type * from "./instance.js";
export type * from "./ledger.js";
export type * from "./obligation.js";
export type * from "./role.js";
export type * from "./run.js";
export type * from "./template.js";
export type * from "./transition.js";
export type {
  WrkfActionFacade,
  WrkfCheckFacade,
  WrkfEffectFacade,
  WrkfEvidenceFacade,
  WrkfFacade,
  WrkfHookFacade,
  WrkfInstanceFacade,
  WrkfLedgerFacade,
  WrkfObligationFacade,
  WrkfRoleFacade,
  WrkfRunFacade,
  WrkfSupervisorFacade,
  WrkfTransitionFacade,
  WrkfWatchFacade,
  WrkfWorkflowFacade,
} from "./facade.js";
