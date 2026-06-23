/**
 * @wrkq/client/wrkq — wrkq namespace types + facade type only.
 *
 * No concrete client lives here; construct it via `createClient` from the root
 * package (spec §7.6).
 */

export type * from "./types.js";
export type {
  WrkqAdminFacade,
  WrkqAttachmentFacade,
  WrkqCommentFacade,
  WrkqContainerFacade,
  WrkqFacade,
  WrkqIndexFacade,
  WrkqLegacyActorFacade,
  WrkqRelationFacade,
  WrkqSearchFacade,
  WrkqTaskFacade,
  WrkqWebhookFacade,
  WrkqWorkflowFacade,
} from "./facade.js";
