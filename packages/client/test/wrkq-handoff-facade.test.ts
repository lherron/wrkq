/**
 * wrkq-handoff-facade.test.ts — wrkq handoff facade (T-05117).
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { FakeTransport } from "../src/testing/fake-transport";
import type { WrkqHandoff } from "../src/wrkq/handoff";
import { clientWith } from "./support/client-fixtures";

describe("wrkq handoff facade", () => {
  test("handoff family forwards create/get/listView/acknowledge with explicit scope", async () => {
    const MOCK_HANDOFF: WrkqHandoff = {
      uuid: "h-u-1",
      id: "H-00001",
      scope_ref: "agent:cody:project:wrkq",
      scope_kind: "project",
      agent_id: "cody",
      project_id: "wrkq",
      agent_actor_uuid: null,
      project_container_uuid: null,
      created_by_agent_id: "cody",
      created_by_actor_uuid: null,
      title: "Next steps",
      body: "carry-over notes",
      status: "pending",
      idempotency_key: null,
      acknowledged_at: null,
      acknowledged_by_agent_id: null,
      acknowledged_by_actor_uuid: null,
      acknowledgement_note: null,
      meta: null,
      etag: 1,
      created_at: "2026-06-23T00:00:00Z",
      updated_at: "2026-06-23T00:00:00Z",
    };
    const transport = new FakeTransport()
      .onResult("wrkq.handoff.create", { handoff: MOCK_HANDOFF, idempotentReplay: false })
      .onResult("wrkq.handoff.get", MOCK_HANDOFF)
      .onResult("wrkq.handoff.listView", { items: [MOCK_HANDOFF], nextCursor: "cur-1" })
      .onResult("wrkq.handoff.acknowledge", {
        ...MOCK_HANDOFF,
        status: "acknowledged",
        acknowledged_at: "2026-06-23T01:00:00Z",
        acknowledged_by_agent_id: "cody",
        acknowledgement_note: "loaded",
        etag: 2,
      });
    const client = await clientWith(transport);

    const created = await client.wrkq.handoff.create({
      scopeRef: "agent:cody:project:wrkq",
      agentId: "cody",
      projectId: "wrkq",
      title: "Next steps",
      body: "carry-over notes",
      idempotencyKey: "k1",
    });
    const got = await client.wrkq.handoff.get({ handoff: "H-00001" });
    const listed = await client.wrkq.handoff.listView({
      scopeRef: "agent:cody:project:wrkq",
      status: "pending",
      limit: 50,
    });
    const acked = await client.wrkq.handoff.acknowledge({
      handoff: "H-00001",
      actorAgentId: "cody",
      principalRef: "agent:cody",
      scopeRef: "agent:cody:project:wrkq",
      note: "loaded",
      ifMatch: 1,
    });

    expect(transport.capturedRequests.map((r) => r.method)).toEqual([
      "wrkq.handoff.create",
      "wrkq.handoff.get",
      "wrkq.handoff.listView",
      "wrkq.handoff.acknowledge",
    ]);
    // create forwards the EXPLICIT caller-resolved scope (server reads no env).
    expect(transport.capturedRequests[0]!.params).toMatchObject({
      scopeRef: "agent:cody:project:wrkq",
      agentId: "cody",
      projectId: "wrkq",
      title: "Next steps",
      idempotencyKey: "k1",
    });
    expect(created.handoff.id).toBe("H-00001");
    expect(created.idempotentReplay).toBe(false);
    // DTO keys are DELIBERATELY snake_case (legacy handoffJSON byte-parity).
    expect(got.scope_ref).toBe("agent:cody:project:wrkq");
    expect(got.created_by_agent_id).toBe("cody");
    expect(listed.items[0]!.id).toBe("H-00001");
    expect(listed.nextCursor).toBe("cur-1");
    // acknowledge forwards the resolved acting identity + ifMatch CAS.
    expect(transport.capturedRequests[3]!.params).toMatchObject({
      handoff: "H-00001",
      actorAgentId: "cody",
      scopeRef: "agent:cody:project:wrkq",
      ifMatch: 1,
    });
    expect(acked.status).toBe("acknowledged");
    expect(acked.etag).toBe(2);
  });
});
