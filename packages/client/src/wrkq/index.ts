/**
 * @wrkq/client/wrkq — wrkq namespace types + facade type only.
 *
 * No concrete client lives here; construct it via `createClient` from the root
 * package (spec §7.6).
 */

export type * from "./attachment.js";
export type * from "./campaign.js";
export type * from "./collaboration.js";
export type * from "./comment.js";
export type * from "./container.js";
export type * from "./handoff.js";
export type * from "./listing.js";
export type * from "./project.js";
export type * from "./project-event.js";
export type * from "./promise.js";
export type * from "./relation.js";
export type * from "./search.js";
export type * from "./task.js";
export type * from "./timeline.js";
export type * from "./webhook.js";
export type * from "./workflow-binding.js";
export type {
  WrkqAdminFacade,
  WrkqAttachmentFacade,
  WrkqCommentFacade,
  WrkqContainerFacade,
  WrkqEnvelopeFacade,
  WrkqFacade,
  WrkqIndexFacade,
  WrkqPromiseFacade,
  WrkqProjectEventFacade,
  WrkqRelationFacade,
  WrkqRoomFacade,
  WrkqSearchFacade,
  WrkqTaskFacade,
  WrkqWebhookFacade,
  WrkqWorkflowFacade,
} from "./facade.js";
