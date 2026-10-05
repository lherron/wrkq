/**
 * wrkq/collaboration.ts — collaboration ledger DTOs: rooms and envelopes
 * (wrkq.room.*, wrkq.envelope.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

// wrkq owns collaboration; HRC is a consumer (T-07612 §2). Every HRC identifier
// carried here — node, runtimeId, hostSessionId, generation, runId — is an
// OPAQUE STRING to wrkq. It never interprets one and never imports hrc.

export type WrkqRoomKind = "campaign" | "task" | "project" | "adhoc";
/**
 * A room's WORK, projected at read time from the task/campaign it is keyed by.
 * Ad-hoc rooms anchor on no work and are always `open`. It NEVER gates: a say
 * into any room you can resolve writes.
 */
export type WrkqRoomWork = "open" | "terminal";

/**
 * A room's ACTIVITY, projected at read time by FIRST MATCH over
 * `lastActivityAt`: `stale` iff work is terminal and the last activity is older
 * than 4h, else `active` under 24h, else `quiet`. Single-valued and total —
 * every room has exactly one value at every instant.
 */
export type WrkqRoomActivity = "active" | "quiet" | "stale";
export type WrkqEnvelopeObligation = "reply_required" | "fyi" | "none";
export type WrkqEnvelopeState =
  | "pending"
  | "presented"
  | "acked"
  | "deferred"
  | "failed"
  | "expired"
  | "withdrawn";
export type WrkqEnvelopeFailureReason =
  "runtime_terminated" | "ignored" | "undeliverable" | "legacy";
export type WrkqRoomMemberSource = "spoke" | "addressed" | "joined";

export interface WrkqRoomWorkRef {
  type: "task" | "container";
  uuid: string;
  id: string;
  path: string;
}

/**
 * The other room a task/campaign pair holds. A task that later joins a campaign
 * routes new says to the campaign room while its own room stays readable —
 * linked both ways, never merged.
 */
export interface WrkqRoomLink {
  relation: string;
  key: string;
  uuid: string;
  kind: WrkqRoomKind;
}

export interface WrkqRoom {
  uuid: string;
  /** Ad-hoc rooms only; a derived room's key IS its work identity. */
  id?: string;
  /** The room key: `T-xxxxx`, a container path, or `R-xxxxx`. */
  key: string;
  kind: WrkqRoomKind;
  /**
   * READ-TIME PROJECTIONS. Render them; never gate on them. A room has no
   * lifecycle state, so nothing here can refuse a say, exclude an obligation
   * from `pendingView`, or stop the stop hook from holding a seat.
   */
  work: WrkqRoomWork;
  activity: WrkqRoomActivity;
  /**
   * Operator discovery labels (`hidden` today). A label changes what the
   * DEFAULT `room.list` returns and nothing else; any principal may set it.
   */
  labels: string[];
  workRef: WrkqRoomWorkRef | null;
  links: WrkqRoomLink[];
  openedByPrincipalRef: string;
  openedAt: string;
  /**
   * The activity clock: `max(openedAt, newest envelope, newest member join)`.
   * `openedAt` always exists, so it is defined for every room including one
   * that has never carried a message.
   */
  lastActivityAt: string;
  openSubtaskCount: number;
  memberCount: number;
  messageCount: number;
  etag: number;
  createdAt: string;
  updatedAt: string;
}

/** One end of an envelope. `scopeRef` is absent for a scope-less principal. */
export interface WrkqEnvelopeParty {
  principalRef: string;
  scopeRef?: string;
}

/** One presentation receipt: the join between wrkq and HRC's execution world. */
export interface WrkqEnvelopePresentation {
  memberRef: string;
  node?: string;
  runtimeId?: string;
  hostSessionId?: string;
  generation?: string;
  runId?: string;
  driveAttemptId?: string;
  /** Broker input that accepted this presentation; opaque to wrkq. */
  inputId?: string;
  /**
   * HRC's own class for HOW this delivery landed — `admitted_into_active_turn`,
   * `presented_to_live_harness`, `started_fresh_turn`, `kicker` today. wrkq
   * stores and returns it and never interprets it, so HRC can add a class
   * without a wrkq change (T-07638).
   */
  deliveryOutcome?: string;
  presentedAt: string;
}

export interface WrkqEnvelope {
  uuid: string;
  /** `EN-xxxxx`. An INTERNAL row id: the injected presentation never shows it. */
  id: string;
  /** Numeric EN ordinal used by exclusive collaboration cursors. */
  messageSeq: number;
  roomUuid: string;
  roomKey: string;
  roomKind: WrkqRoomKind;
  /** Shared by the envelopes one say fanned out to. */
  groupId?: string;
  from: WrkqEnvelopeParty;
  to: WrkqEnvelopeParty | null;
  /**
   * The exact `--to` token that answers THIS envelope: the sender's scope
   * handle, or its principal when it has none. Print it verbatim. A bare name
   * in its place resolves by the room's shape and can address a seat that never
   * asked, leaving the real obligation to fail unanswered (T-07638).
   */
  replyTo: string;
  obligation: WrkqEnvelopeObligation;
  body: string;
  /** Set when the say routed via a task, even into a campaign room. */
  taskId?: string;
  state: WrkqEnvelopeState;
  /** acked | failed | expired | withdrawn. `deferred` is paused. */
  terminal: boolean;
  expiresAt?: string;
  delivery: "queue" | "hold";
  failureReason?: WrkqEnvelopeFailureReason;
  retryAt?: string;
  deferReason?: string;
  /** Terminal acknowledgement disposition from the envelope.acked event. */
  reason?: string;
  terminalActor?: string;
  materializationIntent?: string;
  respondToPrincipalRef?: string;
  retryPromiseId?: string;
  /** The SAY's key, carried by EVERY envelope of a fan-out. */
  idempotencyKey?: string;
  meta: Record<string, unknown>;
  presentedTo: WrkqEnvelopePresentation[];
  etag: number;
  createdAt: string;
  updatedAt: string;
}

export interface WrkqRoomMember {
  memberRef: string;
  memberPrincipalRef: string;
  scoped: boolean;
  source: WrkqRoomMemberSource;
  joinedAt: string;
  leftAt?: string;
  /** Scope-less members have none: they are never presented through a runtime. */
  attendance: WrkqEnvelopePresentation | null;
}

export interface WrkqRoomSayParams {
  /** Routed per T-07612 §4: R-/EN-, T-, container, or agent@project[:task]. */
  ref?: string;
  body: string;
  /** Fans out to one envelope per addressee. Only `to` fires. */
  to?: string[];
  fyi?: boolean;
  /** Server-relative TTL; expires only if never presented. */
  ttl?: string;
  /** Store hold delivery intent for HRC. */
  hold?: boolean;
  /** Exact pending or presented obligations this reply discharges. */
  dischargeEnvelopeIds?: string[];
  /** Force a fresh ad-hoc room instead of reusing the open pair room. */
  new?: boolean;
  respondTo?: string;
  /** Also write the body as a wrkq comment on the room's task. */
  record?: boolean;
  idempotencyKey?: string;
  meta?: Record<string, unknown>;
  principalRef?: string;
  /** The caller's own HRC session handle, when it has one. */
  scopeRef?: string;
}

export interface WrkqRoomSayResult {
  room: WrkqRoom;
  /** The waitable handle; equals the envelope's own id for one addressee. */
  groupId: string;
  envelopes: WrkqEnvelope[];
  /** Envelope ids this say discharged under reply-is-ack. */
  acked: string[];
  recordedCommentId?: string;
  /**
   * Advisory notices. The say already wrote; notices are never errors and have
   * no override flag. `wrkc` prints each to stderr.
   */
  notices?: string[];
  /** The first `notices` entry, retained for one compatibility release. */
  notice?: string;
}

export interface WrkqRoomShowParams {
  room: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomListParams {
  /**
   * Include what the default listing omits: `activity: "stale"` rooms and rooms
   * carrying the `hidden` label. Listing is DISCOVERY — everything it omits is
   * still addressable, and its obligations still gate and wake.
   */
  all?: boolean;
  kind?: WrkqRoomKind;
  /** "me" restricts to rooms the caller's own scope is an active member of. */
  scope?: "me";
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomListResult {
  items: WrkqRoom[];
}

export interface WrkqRoomLogViewParams {
  room: string;
  /** Narrow a campaign room to the traffic that came through one task. */
  task?: string;
  /** Return only the newest N messages, still oldest-first. */
  limit?: number;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomLogView {
  room: WrkqRoom;
  items: WrkqEnvelope[];
}

/**
 * Sets or clears the `hidden` discovery label. Any principal may call it: what
 * a listing shows is not an ownership boundary.
 */
export interface WrkqRoomLabelParams {
  room: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomMemberParams {
  room: string;
  /** Omit on join/leave to mean the caller's own scope. */
  member?: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomMembersViewParams {
  room: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqRoomMembersView {
  room: WrkqRoom;
  items: WrkqRoomMember[];
}

/**
 * The BIRTH ENVELOPE request. It carries the TARGET scope and nothing else: the
 * sender is read off the ledger row, so a caller cannot steer which node a
 * virgin scope is born on (T-07655).
 */
export interface WrkqEnvelopeBirthEnvelopeParams {
  scopeRef: string;
}

/**
 * The birth envelope of a target scope: the lowest-seq `reply_required`
 * envelope addressed to it, in ANY state. `null` means nothing has ever fired
 * at the scope — fyi never summons and is outside the domain.
 */
export interface WrkqEnvelopeBirth {
  envelopeId: string;
  /** The envelope's ledger ordinal, the number its `EN-` id is minted from. */
  seq: number;
  from: WrkqEnvelopeParty;
}

export interface WrkqEnvelopeShowParams {
  envelope: string;
  principalRef?: string;
  scopeRef?: string;
}

interface WrkqEnvelopeMemberPageSharedParams {
  /** Exact stored scope handle, or a scope-less `agent:<id>` principal. */
  memberRef: string;
  /** Required bounded page size, from 1 through 500. */
  limit: number;
  /** Fence follow-up reads against collaboration-ledger replacement. */
  expectedLedgerIncarnation?: string;
  principalRef?: string;
  scopeRef?: string;
}

export type WrkqEnvelopeMemberPageParams = WrkqEnvelopeMemberPageSharedParams &
  (
    | {
        /** Exclusive reverse-history position. */
        beforeMessageSeq: number;
        afterMessageSeq?: never;
      }
    | {
        /** Exclusive forward-catch-up position. */
        beforeMessageSeq?: never;
        afterMessageSeq: number;
      }
  );

export interface WrkqEnvelopeMemberPage {
  ledgerIncarnation: string;
  headMessageSeq: number;
  hasMoreBefore: boolean;
  hasMoreAfter: boolean;
  /** Always chronological, in both cursor directions. */
  items: WrkqEnvelope[];
}

export interface WrkqEnvelopeInboxViewParams {
  scopeRef?: string;
  includeFailed?: boolean;
  principalRef?: string;
}

export interface WrkqEnvelopeInboxGroup {
  room: WrkqRoom;
  items: WrkqEnvelope[];
}

/**
 * fyi is never listed here: it carries no obligation.
 *
 * Every group is a live obligation that gates its addressee's turn: no room
 * projection excuses one. A group whose `room.work` is `"terminal"` is CONTEXT
 * worth rendering — the seat that asked may have moved on — not a separate
 * class of mail.
 */
export interface WrkqEnvelopeInboxView {
  scopeRef?: string;
  principalRef: string;
  groups: WrkqEnvelopeInboxGroup[];
  deferred: WrkqEnvelope[];
  failed: WrkqEnvelope[];
  sentFailed: WrkqEnvelope[];
  sentExpired: WrkqEnvelope[];
  sentWithdrawn: WrkqEnvelope[];
}

export interface WrkqEnvelopeWithdrawParams {
  envelope: string;
  group?: boolean;
  reason?: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqEnvelopeWithdrawRefusal {
  envelopeId: string;
  reason: string;
  state?: WrkqEnvelopeState;
  presentation?: WrkqEnvelopePresentation;
}

export interface WrkqEnvelopeWithdrawResult {
  withdrawn: WrkqEnvelope[];
  refused: WrkqEnvelopeWithdrawRefusal[];
}

export interface WrkqEnvelopeDeferParams {
  envelope: string;
  reason: string;
  /** Relative retry time resolved by the server; backed by a wrkq promise. */
  retryAfter?: string;
  retryAt?: string;
  ifMatch?: number;
  principalRef?: string;
  scopeRef?: string;
}

/**
 * Defaults to the operator ack. `consumed_by_wait` is caller-scoped and only
 * accepts the exact addressee's pending/presented reply-required envelope.
 */
export interface WrkqEnvelopeAckParams {
  envelopes: string[];
  note?: string;
  reason?: "consumed_by_wait";
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqEnvelopePresentParams {
  envelope: string;
  /** Compute the presentation projection without writing a receipt or advancing state. */
  preview?: boolean;
  memberRef?: string;
  node?: string;
  runtimeId?: string;
  hostSessionId?: string;
  generation?: string;
  runId?: string;
  /** One drive attempt presents an envelope exactly once. */
  driveAttemptId?: string;
  /** Broker input that accepted this presentation; ignored when previewing. */
  inputId?: string;
  /** Optional. HRC's class for how this delivery landed; absent stays null. */
  deliveryOutcome?: string;
  principalRef?: string;
  scopeRef?: string;
}

export interface WrkqEnvelopePresentResult {
  envelope: WrkqEnvelope;
  recorded: boolean;
  /**
   * The §7 `history:` cue decision, keyed to the RUNTIME and not the
   * generation: /quit clears continuation without rotating the generation, so
   * every post-quit runtime is cold and gets the cue.
   */
  historyHint: boolean;
  messageCount: number;
  lastMessageAt?: string;
}

export interface WrkqEnvelopePendingViewParams {
  scopes?: string[];
  /**
   * Additionally report pending `fyi` envelopes in `items`. A request
   * parameter, not a feature flag: the default read is the wake set and stays
   * obligation-only. A fyi never enters `blocking` and never summons — gating
   * presentation to a live generation is the consumer's half of §5.
   */
  includeFyi?: boolean;
  principalRef?: string;
  scopeRef?: string;
}

/**
 * The kicker wake set AND the stop-hook predicate in one read model.
 *
 * The read is UNIFORM over rooms: an obligation wakes and gates whatever its
 * room's `work`, `activity`, or `hidden` label says. Rooms have no lifecycle
 * that can refuse a say, so the addressee always has a reply path — excluding
 * such mail would silently strand a follow-up on finished work (T-07642).
 */
export interface WrkqEnvelopePendingView {
  /**
   * Standing reply_required envelopes, plus pending `fyi` envelopes when the
   * caller asked for `includeFyi`.
   */
  items: WrkqEnvelope[];
  /** Envelope ids that must be replied or deferred before a turn may end. */
  blocking: string[];
  /** How many due deferrals this read's sweep returned to pending. */
  repended: number;
}

export interface WrkqEnvelopeFailParams {
  envelope: string;
  reason: Exclude<WrkqEnvelopeFailureReason, "legacy">;
  /** Optional sender-facing reason, carried on the envelope.failed event (≤2 KiB, truncated). */
  detail?: string;
  runtime?: string;
  principalRef?: string;
  scopeRef?: string;
}
