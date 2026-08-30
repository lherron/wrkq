import { describe, expect, test } from "bun:test";
import { createClient, type WorkClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";
import type {
  WrkqEnvelope,
  WrkqEnvelopeMemberPage,
  WrkqEnvelopeMemberPageParams,
  WrkqEnvelopeInboxView,
  WrkqEnvelopePendingView,
  WrkqEnvelopePresentResult,
  WrkqRoom,
  WrkqRoomLogView,
  WrkqRoomMembersView,
  WrkqRoomSayResult,
} from "../src/wrkq/types";

type Equal<Left, Right> =
  (<Value>() => Value extends Left ? 1 : 2) extends
  (<Value>() => Value extends Right ? 1 : 2)
    ? true
    : false;
type Expect<Value extends true> = Value;

type _MemberPageFacadeParamIdentity = Expect<
  Equal<
    Parameters<WorkClient["wrkq"]["envelope"]["memberPage"]>[0],
    WrkqEnvelopeMemberPageParams
  >
>;

// Compile-time acceptance coverage. TypeScript rejects an unused expected-error
// directive, so these guards fail if the public type admits neither or both
// cursors.
function _memberPageCursorXor(params: WrkqEnvelopeMemberPageParams) {
  const beforeOnly: WrkqEnvelopeMemberPageParams = {
    memberRef: "cody@wrkq:T-07723",
    beforeMessageSeq: 10,
    limit: 25,
  };
  const afterOnly: WrkqEnvelopeMemberPageParams = {
    memberRef: "cody@wrkq:T-07723",
    afterMessageSeq: 10,
    limit: 25,
  };
  // @ts-expect-error exactly one cursor is required
  const neither: WrkqEnvelopeMemberPageParams = {
    memberRef: "cody@wrkq:T-07723",
    limit: 25,
  };
  // @ts-expect-error beforeMessageSeq and afterMessageSeq are exclusive
  const both: WrkqEnvelopeMemberPageParams = {
    memberRef: "cody@wrkq:T-07723",
    beforeMessageSeq: 10,
    afterMessageSeq: 10,
    limit: 25,
  };
  return { params, beforeOnly, afterOnly, neither, both };
}

const ROOM: WrkqRoom = {
  uuid: "room-uuid",
  key: "T-07613",
  kind: "task",
  work: "open",
  activity: "active",
  labels: [],
  workRef: {
    type: "task",
    uuid: "task-uuid",
    id: "T-07613",
    path: "wrkq/rooms/wave1",
  },
  links: [],
  openedByPrincipalRef: "agent:clod",
  openedAt: "2026-08-27T00:00:00Z",
  lastActivityAt: "2026-08-27T00:05:00Z",
  memberCount: 2,
  messageCount: 1,
  etag: 1,
  createdAt: "2026-08-27T00:00:00Z",
  updatedAt: "2026-08-27T00:05:00Z",
};

const ENVELOPE: WrkqEnvelope = {
  uuid: "envelope-uuid",
  id: "EN-00001",
  messageSeq: 1,
  roomUuid: ROOM.uuid,
  roomKey: ROOM.key,
  roomKind: "task",
  groupId: "EN-00001",
  from: { principalRef: "agent:clod", scopeRef: "clod@wrkq:T-07613" },
  to: { principalRef: "agent:cody", scopeRef: "cody@wrkq:T-07613" },
  replyTo: "clod@wrkq:T-07613",
  obligation: "reply_required",
  body: "wave 1 is up",
  taskId: "T-07613",
  state: "presented",
  terminal: false,
  idempotencyKey: "acp:hrc-message:m-1",
  meta: {},
  presentedTo: [
    {
      memberRef: "cody@wrkq:T-07613",
      node: "mini",
      runtimeId: "runtime-A",
      generation: "49",
      inputId: "input-A",
      deliveryOutcome: "admitted_into_active_turn",
      presentedAt: "2026-08-27T00:05:00Z",
    },
  ],
  etag: 2,
  createdAt: "2026-08-27T00:04:00Z",
  updatedAt: "2026-08-27T00:05:00Z",
};

const SAY_RESULT: WrkqRoomSayResult = {
  room: ROOM,
  groupId: "EN-00001",
  envelopes: [ENVELOPE],
  acked: [],
  notices: ["this looks like T-07613 work"],
  notice: "this looks like T-07613 work",
};

const LOG_VIEW: WrkqRoomLogView = { room: ROOM, items: [ENVELOPE] };
const MEMBER_PAGE: WrkqEnvelopeMemberPage = {
  ledgerIncarnation: "ledger-1",
  headMessageSeq: 1,
  hasMoreBefore: false,
  hasMoreAfter: false,
  items: [ENVELOPE],
};
const MEMBERS_VIEW: WrkqRoomMembersView = {
  room: ROOM,
  items: [
    {
      memberRef: "clod@wrkq:T-07613",
      memberPrincipalRef: "agent:clod",
      scoped: true,
      source: "spoke",
      joinedAt: "2026-08-27T00:04:00Z",
      attendance: null,
    },
  ],
};
const INBOX_VIEW: WrkqEnvelopeInboxView = {
  scopeRef: "cody@wrkq:T-07613",
  principalRef: "agent:cody",
  groups: [{ room: ROOM, items: [ENVELOPE] }],
  deferred: [],
  failed: [],
  sentFailed: [],
};
const PRESENT_RESULT: WrkqEnvelopePresentResult = {
  envelope: ENVELOPE,
  recorded: true,
  historyHint: true,
  messageCount: 14,
  lastMessageAt: "2026-08-27T00:04:00Z",
};
const PENDING_VIEW: WrkqEnvelopePendingView = {
  items: [ENVELOPE],
  blocking: [ENVELOPE.id],
  repended: 0,
};

describe("wrkq.room facade", () => {
  test("forwards the complete room RPC family with typed results", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.room.say", SAY_RESULT)
      .onResult("wrkq.room.show", ROOM)
      .onResult("wrkq.room.list", { items: [ROOM] })
      .onResult("wrkq.room.logView", LOG_VIEW)
      .onResult("wrkq.room.hide", { ...ROOM, labels: ["hidden"] })
      .onResult("wrkq.room.unhide", ROOM)
      .onResult("wrkq.room.join", MEMBERS_VIEW)
      .onResult("wrkq.room.leave", MEMBERS_VIEW)
      .onResult("wrkq.room.membersView", MEMBERS_VIEW);
    const client = await createClient({ transport, autoInitialize: false });

    const say = {
      ref: "T-07613",
      body: "wave 1 is up",
      to: ["cody"],
      idempotencyKey: "acp:hrc-message:m-1",
      principalRef: "agent:clod",
      scopeRef: "clod@wrkq:T-07613",
    };
    const show = { room: "T-07613" };
    const logView = { room: "T-07613", task: "T-07613", limit: 20 };
    const label = { room: "T-07613" };
    const member = { room: "T-07613", member: "mable@wrkq:primary" };

    const said = await client.wrkq.room.say(say);
    expect(said.groupId).toBe("EN-00001");
    expect(said.envelopes[0]?.idempotencyKey).toBe("acp:hrc-message:m-1");
    expect(said.notices).toEqual(["this looks like T-07613 work"]);
    await client.wrkq.room.show(show);
    expect(
      (
        await client.wrkq.room.list({
          all: true,
          scope: "me",
          scopeRef: "cody@wrkq:T-07613",
        })
      ).items,
    ).toHaveLength(1);
    expect((await client.wrkq.room.logView(logView)).items).toHaveLength(1);

    // hide/unhide is a DISCOVERY label, not a lifecycle: it moves `labels`, and
    // work/activity — the two read-time projections — are untouched by it.
    const hidden = await client.wrkq.room.hide(label);
    expect(hidden.labels).toEqual(["hidden"]);
    expect(hidden.work).toBe("open");
    expect((await client.wrkq.room.unhide(label)).labels).toEqual([]);
    await client.wrkq.room.join(member);
    await client.wrkq.room.leave(member);
    await client.wrkq.room.membersView(show);

    expect(
      transport.capturedRequests.map(({ method, params }) => ({
        method,
        params,
      })),
    ).toEqual([
      { method: "wrkq.room.say", params: say },
      { method: "wrkq.room.show", params: show },
      {
        method: "wrkq.room.list",
        params: { all: true, scope: "me", scopeRef: "cody@wrkq:T-07613" },
      },
      { method: "wrkq.room.logView", params: logView },
      { method: "wrkq.room.hide", params: label },
      { method: "wrkq.room.unhide", params: label },
      { method: "wrkq.room.join", params: member },
      { method: "wrkq.room.leave", params: member },
      { method: "wrkq.room.membersView", params: show },
    ]);
  });

  test("list defaults to an empty parameter object", async () => {
    const transport = new FakeTransport().onResult("wrkq.room.list", {
      items: [],
    });
    const client = await createClient({ transport, autoInitialize: false });
    await client.wrkq.room.list();
    expect(transport.capturedRequests.map((request) => request.params)).toEqual(
      [{}],
    );
  });
});

describe("wrkq.envelope facade", () => {
  test("forwards the complete envelope RPC family, HRC-facing verbs included", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.envelope.show", ENVELOPE)
      .onResult("wrkq.envelope.memberPage", MEMBER_PAGE)
      .onResult("wrkq.envelope.inboxView", INBOX_VIEW)
      .onResult("wrkq.envelope.defer", { ...ENVELOPE, state: "deferred" })
      .onResult("wrkq.envelope.ack", LOG_VIEW)
      .onResult("wrkq.envelope.present", PRESENT_RESULT)
      .onResult("wrkq.envelope.pendingView", PENDING_VIEW)
      .onResult("wrkq.envelope.fail", {
        ...ENVELOPE,
        state: "failed",
        terminal: true,
        failureReason: "runtime_terminated",
      })
      .onResult("wrkq.envelope.birthEnvelope", {
        envelopeId: "EN-00001",
        seq: 1,
        from: { principalRef: "agent:clod", scopeRef: "clod@wrkq:T-07613" },
      });
    const client = await createClient({ transport, autoInitialize: false });

    const show = { envelope: "EN-00001" };
    const memberPage = {
      memberRef: "cody@wrkq:T-07613",
      beforeMessageSeq: 2,
      limit: 1,
      expectedLedgerIncarnation: "ledger-1",
    };
    const inbox = { scopeRef: "cody@wrkq:T-07613", includeFailed: true };
    const defer = {
      envelope: "EN-00001",
      reason: "after the build",
      retryAfter: "2h",
      scopeRef: "cody@wrkq:T-07613",
    };
    const ack = {
      envelopes: ["EN-00001"],
      note: "handled",
      principalRef: "agent:lance",
    };
    const present = {
      envelope: "EN-00001",
      node: "mini",
      runtimeId: "runtime-A",
      generation: "49",
      driveAttemptId: "drive-1",
      preview: false,
      inputId: "input-A",
      // HRC's own class for how the delivery landed, held opaquely by wrkq.
      deliveryOutcome: "admitted_into_active_turn",
      principalRef: "agent:hrc",
    };
    const pending = {
      scopes: ["cody@wrkq:T-07613"],
      principalRef: "agent:hrc",
    };
    const fail = {
      envelope: "EN-00001",
      reason: "runtime_terminated" as const,
      runtime: "runtime-A",
      principalRef: "agent:hrc",
    };
    // The birth-envelope request carries the TARGET and nothing else: the
    // sender comes off the ledger row, so a caller cannot steer which node a
    // virgin scope is born on (T-07655).
    const birth = { scopeRef: "cody@wrkq:T-07613" };

    await client.wrkq.envelope.show(show);
    expect((await client.wrkq.envelope.memberPage(memberPage)).items[0]?.messageSeq).toBe(1);

    // The inbox names the EXACT token that answers each obligation: HRC's §7
    // reply line prints it verbatim rather than shortening it to a bare name,
    // which resolves per room and can address a seat that never asked (T-07638).
    const standingInbox = await client.wrkq.envelope.inboxView(inbox);
    expect(standingInbox.groups).toHaveLength(1);
    expect(standingInbox.groups[0]!.items[0]!.replyTo).toBe(
      "clod@wrkq:T-07613",
    );
    expect((await client.wrkq.envelope.defer(defer)).state).toBe("deferred");
    expect((await client.wrkq.envelope.ack(ack)).items).toHaveLength(1);

    // The presentation receipt answers the §7 history cue, keyed to the RUNTIME.
    const presented = await client.wrkq.envelope.present(present);
    expect(presented.historyHint).toBe(true);
    expect(presented.recorded).toBe(true);
    expect(presented.envelope.presentedTo[0]!.inputId).toBe("input-A");
    expect(presented.envelope.presentedTo[0]!.deliveryOutcome).toBe(
      "admitted_into_active_turn",
    );

    // One read model serves both the kicker wake set and the stop-hook predicate.
    const standing = await client.wrkq.envelope.pendingView(pending);
    expect(standing.items).toHaveLength(1);
    expect(standing.blocking).toEqual(["EN-00001"]);

    expect((await client.wrkq.envelope.fail(fail)).failureReason).toBe(
      "runtime_terminated",
    );

    const born = await client.wrkq.envelope.birthEnvelope(birth);
    expect(born?.envelopeId).toBe("EN-00001");
    expect(born?.from.scopeRef).toBe("clod@wrkq:T-07613");

    expect(
      transport.capturedRequests.map(({ method, params }) => ({
        method,
        params,
      })),
    ).toEqual([
      { method: "wrkq.envelope.show", params: show },
      { method: "wrkq.envelope.memberPage", params: memberPage },
      { method: "wrkq.envelope.inboxView", params: inbox },
      { method: "wrkq.envelope.defer", params: defer },
      { method: "wrkq.envelope.ack", params: ack },
      { method: "wrkq.envelope.present", params: present },
      { method: "wrkq.envelope.pendingView", params: pending },
      { method: "wrkq.envelope.fail", params: fail },
      { method: "wrkq.envelope.birthEnvelope", params: birth },
    ]);
  });

  test("birthEnvelope is null when nothing ever fired at the scope", async () => {
    // A scope that has only ever been sent fyi has no birth envelope: fyi never
    // summons, so there is nothing to designate a birth node from and the
    // registry falls back to today's tier 5 rather than inventing a home.
    const transport = new FakeTransport().onResult(
      "wrkq.envelope.birthEnvelope",
      null,
    );
    const client = await createClient({ transport, autoInitialize: false });

    expect(
      await client.wrkq.envelope.birthEnvelope({
        scopeRef: "cody@wrkq:T-07613",
      }),
    ).toBeNull();
  });

  test("pendingView includeFyi forwards the opt-in and keeps fyi out of blocking", async () => {
    const fyi: WrkqEnvelope = {
      ...ENVELOPE,
      uuid: "envelope-uuid-fyi",
      id: "EN-00002",
      groupId: "EN-00002",
      obligation: "fyi",
      body: "heads up",
      state: "pending",
    };
    const transport = new FakeTransport().onResult(
      "wrkq.envelope.pendingView",
      {
        items: [ENVELOPE, fyi],
        blocking: [ENVELOPE.id],
        repended: 0,
      },
    );
    const client = await createClient({ transport, autoInitialize: false });

    const params = {
      scopes: ["cody@wrkq:T-07613"],
      includeFyi: true,
      principalRef: "agent:hrc",
    };
    const view = await client.wrkq.envelope.pendingView(params);

    expect(view.items.map((item) => item.obligation)).toEqual([
      "reply_required",
      "fyi",
    ]);
    // fyi carries no obligation, so it never refuses a turn end and never summons.
    expect(view.blocking).toEqual([ENVELOPE.id]);
    expect(
      transport.capturedRequests.map(({ method, params: sent }) => ({
        method,
        params: sent,
      })),
    ).toEqual([{ method: "wrkq.envelope.pendingView", params }]);
  });

  test("inboxView and pendingView default to empty parameter objects", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.envelope.inboxView", INBOX_VIEW)
      .onResult("wrkq.envelope.pendingView", PENDING_VIEW);
    const client = await createClient({ transport, autoInitialize: false });
    await client.wrkq.envelope.inboxView();
    await client.wrkq.envelope.pendingView();
    expect(transport.capturedRequests.map((request) => request.params)).toEqual(
      [{}, {}],
    );
  });
});
