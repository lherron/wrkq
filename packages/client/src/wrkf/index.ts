/**
 * @wrkq/client/wrkf — wrkf namespace types + facade type only.
 *
 * No concrete client lives here; construct it via `createClient` from the root
 * package (spec §7.6).
 */

export type * from "./types.js";
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
