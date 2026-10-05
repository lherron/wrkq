/**
 * wrkq/timeline.ts — wrkq.container.timelineView DTOs.
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

import type {
  WrkqCampaignFootprint,
  WrkqCampaignMemberDiagnostic,
  WrkqCampaignProject,
} from "./campaign.js";
import type { WrkqEnvelopeObligation, WrkqRoomKind } from "./collaboration.js";
import type { WrkqCommentKind } from "./comment.js";
import type { WrkqCampaignState, WrkqContainer } from "./container.js";
import type { WrkqTaskState } from "./task.js";

export interface WrkqContainerTimelineViewParams {
  container: string;
  cursor?: string;
  limit?: number;
  scope?: "container" | "subtree";
  types?: string[];
  /** Include turn.* facts hidden by default unless types names them. */
  allTypes?: boolean;
  task?: string;
  since?: string;
  /** Exclusive upper bound on the server timestamp (RFC3339). */
  before?: string;
  entriesOnly?: boolean;
  tail?: boolean;
  /**
   * Delivery direction: "asc" (default, oldest first) or "desc" (newest
   * first). A cursor carries its own direction and is authoritative; an order
   * that contradicts it is refused rather than silently reinterpreted. "desc"
   * is incompatible with tail, which follows appends and is ascending by
   * construction.
   */
  order?: "asc" | "desc";
}

/** The timeline's container identity: a WrkqContainer without its campaign adornment. */
export type WrkqTimelineContainer = Omit<WrkqContainer, "campaignState">;

export interface WrkqCampaignAdornment {
  state: WrkqCampaignState;
  archived: boolean;
  archivedAt?: string;
}

export interface WrkqTimelineMember {
  uuid: string;
  id: string;
  path: string;
  title: string;
  state: WrkqTaskState;
  outcome?: string;
  membership: "resident" | "enrolled";
  project: WrkqCampaignProject;
}

export interface WrkqTimelineRollup {
  terminal: number;
  total: number;
}

export interface WrkqTimelineComment {
  id?: string;
  kind?: WrkqCommentKind;
  body: string;
  meta?: Record<string, unknown>;
}

/**
 * One room message projected into the timeline. This is the leader of a
 * fan-out group, not one entry per addressee: a single `say` to N addressees
 * writes N envelopes sharing one group id, and the timeline reports the
 * MESSAGE, listing every addressee in `to`.
 */
export interface WrkqTimelineMessage {
  envelopeId: string;
  groupId?: string;
  roomId?: string;
  roomKind?: WrkqRoomKind;
  from: string;
  to: string[];
  obligation: WrkqEnvelopeObligation;
  body: string;
}

export interface WrkqTimelineOutcome {
  text: string | null;
}

export interface WrkqTimelineTaskState {
  from?: WrkqTaskState;
  state: WrkqTaskState | "purged";
  sourceEventType:
    | "task.created"
    | "task.updated"
    | "task.archived"
    | "task.deleted"
    | "task.restored"
    | "task.purged";
}

export interface WrkqTimelineRequester {
  principalRef: string;
  scopeRef?: string;
}

/**
 * task.claimed names the new holder; task.claim_released names the holder that
 * was released (the entry's principalRef is the releaser).
 */
export interface WrkqTimelineClaim {
  principalRef?: string;
  scopeRef?: string;
  node?: string;
  generation: number;
  takeOver?: boolean;
  force?: boolean;
}

export interface WrkqTimelineContainerState {
  from: WrkqCampaignState | null;
  to: WrkqCampaignState;
}

interface WrkqTimelineEntryBase {
  eventId: number;
  timestamp: string;
  principalRef?: string;
  scopeRef?: string;
  resourceUuid?: string;
  taskUuid?: string;
  taskId?: string;
  taskPath?: string;
  membership?: "resident" | "enrolled" | "subtree" | "participant";
  campaignUuid: string | null;
  containerUuid?: string;
}

export type WrkqTimelineEntry =
  | (WrkqTimelineEntryBase & {
      type: "comment";
      comment: WrkqTimelineComment;
    })
  | (WrkqTimelineEntryBase & {
      type: "message";
      message: WrkqTimelineMessage;
    })
  | (WrkqTimelineEntryBase & {
      type: "task.outcome";
      outcome: WrkqTimelineOutcome;
    })
  | (WrkqTimelineEntryBase & {
      type: "task.state";
      taskState: WrkqTimelineTaskState;
    })
  | (WrkqTimelineEntryBase & {
      type: "task.created";
      taskState?: WrkqTimelineTaskState;
      requester?: WrkqTimelineRequester;
    })
  | (WrkqTimelineEntryBase & {
      type: "task.claimed" | "task.claim_released";
      claim: WrkqTimelineClaim;
    })
  | (WrkqTimelineEntryBase & {
      type: "task.edited" | "task.moved";
    })
  | (WrkqTimelineEntryBase & {
      type: "container.state";
      containerState: WrkqTimelineContainerState;
    })
  | (WrkqTimelineEntryBase & {
      type: "project.event";
      projectEvent: WrkqTimelineProjectEvent;
    });

export interface WrkqTimelineProjectEvent {
  uuid: string;
  type: string;
  attributes: Record<string, string>;
  principalRef: string | null;
  scopeRef: string | null;
  summary: string;
  occurredAt: string;
}

export interface WrkqContainerTimelineView {
  container: WrkqTimelineContainer;
  campaign: WrkqCampaignAdornment | null;
  members?: WrkqTimelineMember[];
  rollup?: WrkqTimelineRollup;
  missingOutcomes?: WrkqCampaignMemberDiagnostic[];
  footprint?: WrkqCampaignFootprint[];
  lastActivityAt: string;
  decisionTasks?: WrkqTimelineMember[];
  entries: WrkqTimelineEntry[];
  snapshotEventId: number;
  snapshotProjectEventId?: number;
  nextCursor?: string;
}
