/**
 * wrkq/campaign.ts — campaign lifecycle and portfolio DTOs
 * (wrkq.container.campaign*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

import type { WrkqCampaignState, WrkqContainer } from "./container.js";
import type { WrkqTaskState } from "./task.js";

export interface WrkqContainerCampaignConvertParams {
  container: string;
  state?: "draft" | "active";
  description?: string;
  specification?: string;
  labels?: string[];
  expectEtag?: number;
  actor?: string;
}

export interface WrkqContainerCampaignUpdateParams {
  container: string;
  description?: string;
  specification?: string;
  labels?: string[];
  expectEtag?: number;
  actor?: string;
}

export interface WrkqContainerCampaignActivateParams {
  container: string;
  expectEtag?: number;
  actor?: string;
}

export interface WrkqContainerCampaignCloseParams {
  container: string;
  state: "completed" | "cancelled";
  expectEtag?: number;
  actor?: string;
}

export interface WrkqCampaignMemberDiagnostic {
  uuid: string;
  id: string;
  path: string;
  state: WrkqTaskState;
  membership: "resident" | "enrolled";
}

export interface WrkqCampaignTransitionResult {
  container: WrkqContainer;
  previousState: WrkqCampaignState | null;
  campaignState: WrkqCampaignState;
  missingOutcomes: WrkqCampaignMemberDiagnostic[];
  eventId: number;
  eventTimestamp: string;
}

export interface WrkqContainerCampaignPortfolioParams {
  states?: WrkqCampaignState[];
  includeArchived?: boolean;
}

export interface WrkqCampaignProject {
  uuid: string;
  id: string;
  slug: string;
  title: string;
}

export interface WrkqCampaignFootprint {
  project: WrkqCampaignProject;
  memberCount: number;
}

export interface WrkqCampaignPortfolioRow {
  container: WrkqContainer;
  totalMembers: number;
  stateCounts: Record<string, number>;
  residentCount: number;
  enrolledCount: number;
  inProgressCount: number;
  missingOutcomeCount: number;
  footprint: WrkqCampaignFootprint[];
  lastActivityAt: string;
}

export interface WrkqCampaignPortfolio {
  items: WrkqCampaignPortfolioRow[];
}
