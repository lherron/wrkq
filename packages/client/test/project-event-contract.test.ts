import { describe, expect, test } from "bun:test";
import { createClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";

describe("wrkq.projectEvent facade", () => {
  test("forwards project-event methods and timeline v2 parameters", async () => {
    const event = {
      id: 12,
      fid: "PE-00012",
      projectUuid: "project",
      containerUuid: "dir",
      campaignUuid: null,
      taskUuid: "task",
      type: "hrc.server_started",
      source: "hrc",
      node: "max3",
      principalRef: "agent:hrc",
      summary: "started",
      payload: { pid: 42 },
      occurredAt: "2026-09-04T12:00:00Z",
      createdAt: "2026-09-04T12:00:01Z",
    };
    const transport = new FakeTransport()
      .onResult("wrkq.projectEvent.post", {
        id: 12,
        fid: "PE-00012",
        created: true,
      })
      .onResult("wrkq.projectEvent.get", event)
      .onResult("wrkq.projectEvent.typesView", {
        items: [{ type: event.type, count: 1, lastCreatedAt: event.createdAt }],
      })
      .onResult("wrkq.container.timelineView", {
        container: {},
        campaign: null,
        lastActivityAt: event.createdAt,
        entries: [],
        snapshotEventId: 3,
        snapshotProjectEventId: 12,
        nextCursor: "v2",
      });
    const client = await createClient({ transport, autoInitialize: false });

    const post = {
      project: "wrkq",
      task: "T-00001",
      type: event.type,
      source: "hrc",
      summary: "started",
      principalRef: "agent:hrc",
    };
    await client.wrkq.projectEvent.post(post);
    await client.wrkq.projectEvent.get({ projectEvent: event.fid });
    await client.wrkq.projectEvent.typesView({ project: "wrkq" });
    const timeline = {
      container: "wrkq",
      scope: "subtree" as const,
      types: ["hrc.*"],
      task: "T-00001",
      since: "4h",
      entriesOnly: true,
      tail: true,
    };
    await client.wrkq.container.timelineView(timeline);

    expect(
      transport.capturedRequests.map(({ method, params }) => ({
        method,
        params,
      })),
    ).toEqual([
      { method: "wrkq.projectEvent.post", params: post },
      { method: "wrkq.projectEvent.get", params: { projectEvent: event.fid } },
      { method: "wrkq.projectEvent.typesView", params: { project: "wrkq" } },
      { method: "wrkq.container.timelineView", params: timeline },
    ]);
  });
});
