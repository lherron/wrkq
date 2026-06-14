/**
 * @wrkq/client/wrkq — wrkq namespace types + facade type only.
 *
 * No concrete client lives here; construct it via `createClient` from the root
 * package (spec §7.6).
 */

export type * from "./types.js";
export type {
  WrkqAttachmentFacade,
  WrkqCommentFacade,
  WrkqContainerFacade,
  WrkqFacade,
  WrkqRelationFacade,
  WrkqTaskFacade,
  WrkqWorkflowFacade,
} from "./facade.js";
