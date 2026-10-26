/**
 * wrkq-container-facade.test.ts — wrkq container facade: mutations, campaigns, timeline, task counts,
 * project root registry, and global webhooks.
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { FakeTransport } from "../src/testing/fake-transport";
import type {
  WrkqContainerCreateParams,
  WrkqContainerDeleteParams,
  WrkqContainerDeleteRecursiveParams,
  WrkqContainerUpdateParams,
} from "../src/wrkq/container";
import type { WrkqWebhookMutateParams, WrkqWebhookSubscription } from "../src/wrkq/webhook";
import { MOCK_CONTAINER, clientWith } from "./support/client-fixtures";

describe("wrkq container facade", () => {
  test("container mutation facade sends create, update, delete and deleteRecursive with params unchanged", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.container.create", MOCK_CONTAINER)
      .onResult("wrkq.container.update", MOCK_CONTAINER)
      .onResult("wrkq.container.delete", { deleted: true })
      .onResult("wrkq.container.deleteRecursive", { deleted: true });
    const client = await clientWith(transport);

    const create = { path: "project", slug: "child", kind: "directory" } satisfies WrkqContainerCreateParams;
    const update = {
      container: "project",
      patch: { slug: "renamed", title: "Renamed" },
      expectEtag: 1,
      actor: "agent:larry",
      idempotencyKey: "cu1",
    } satisfies WrkqContainerUpdateParams;
    const del = { path: "project/child", expectEtag: 1 } satisfies WrkqContainerDeleteParams;
    // deleteRecursive carries the caller's expected-count guard verbatim.
    const deleteRecursive = {
      path: "project",
      expected: { containers: 2, tasks: 3, attachments: 1, bytes: 42 },
    } satisfies WrkqContainerDeleteRecursiveParams;

    await client.wrkq.container.create(create);
    await client.wrkq.container.update(update);
    await client.wrkq.container.delete(del);
    await client.wrkq.container.deleteRecursive(deleteRecursive);

    expect(transport.capturedRequests.map(({ method, params }) => ({ method, params }))).toEqual([
      { method: "wrkq.container.create", params: create },
      { method: "wrkq.container.update", params: update },
      { method: "wrkq.container.delete", params: del },
      { method: "wrkq.container.deleteRecursive", params: deleteRecursive },
    ]);
  });

  test("campaign facade forwards lifecycle and portfolio methods", async () => {
    const draft = {
      ...MOCK_CONTAINER,
      labels: ["domain:platform"],
      campaignState: "draft" as const,
      etag: 2,
    };
    const active = {
      ...draft,
      description: "brief",
      specification: "ratified spec",
      campaignState: "active" as const,
      etag: 3,
    };
    const transport = new FakeTransport()
      .onResult("wrkq.container.campaignConvert", {
        container: draft,
        previousState: null,
        campaignState: "draft",
        missingOutcomes: [],
        eventId: 10,
        eventTimestamp: "2026-07-23T00:00:00Z",
      })
      .onResult("wrkq.container.campaignActivate", {
        container: active,
        previousState: "draft",
        campaignState: "active",
        missingOutcomes: [],
        eventId: 11,
        eventTimestamp: "2026-07-23T00:00:30Z",
      })
      .onResult("wrkq.container.campaignUpdate", {
        ...active,
        description: "amended brief",
        etag: 4,
      })
      .onResult("wrkq.container.campaignPortfolio", {
        items: [
          {
            container: active,
            totalMembers: 0,
            stateCounts: {},
            residentCount: 0,
            enrolledCount: 0,
            inProgressCount: 0,
            missingOutcomeCount: 0,
            footprint: [],
            lastActivityAt: active.updatedAt,
          },
        ],
      })
      .onResult("wrkq.container.campaignClose", {
        container: { ...active, campaignState: "completed", etag: 5 },
        previousState: "active",
        campaignState: "completed",
        missingOutcomes: [],
        eventId: 12,
        eventTimestamp: "2026-07-23T00:01:00Z",
      });
    const client = await clientWith(transport);

    await client.wrkq.container.campaignConvert({
      container: "project",
      state: "draft",
      description: "brief",
      specification: "ratified spec",
      labels: ["domain:platform"],
      expectEtag: 1,
    });
    await client.wrkq.container.campaignActivate({
      container: "project",
      expectEtag: 2,
    });
    const updated = await client.wrkq.container.campaignUpdate({
      container: "project",
      description: "amended brief",
      expectEtag: 3,
    });
    const portfolio = await client.wrkq.container.campaignPortfolio();
    const closed = await client.wrkq.container.campaignClose({
      container: "project",
      state: "completed",
      expectEtag: 4,
    });

    expect(transport.capturedRequests.map((frame) => frame.method)).toEqual([
      "wrkq.container.campaignConvert",
      "wrkq.container.campaignActivate",
      "wrkq.container.campaignUpdate",
      "wrkq.container.campaignPortfolio",
      "wrkq.container.campaignClose",
    ]);
    expect(transport.capturedRequests[0]!.params).toEqual({
      container: "project",
      state: "draft",
      description: "brief",
      specification: "ratified spec",
      labels: ["domain:platform"],
      expectEtag: 1,
    });
    expect(updated.description).toBe("amended brief");
    expect(portfolio.items).toHaveLength(1);
    expect(closed.campaignState).toBe("completed");
    expect(closed.container.campaignState).toBe("completed");
  });

  test("container timeline facade preserves snapshot pagination and discriminated entries", async () => {
    const transport = new FakeTransport().onResult("wrkq.container.timelineView", {
      container: {
        ...MOCK_CONTAINER,
        description: "plain brief",
        specification: "plain spec",
      },
      campaign: null,
      members: [],
      rollup: { terminal: 0, total: 0 },
      missingOutcomes: [],
      footprint: [],
      lastActivityAt: MOCK_CONTAINER.updatedAt,
      decisionTasks: [],
      entries: [
        {
          type: "task.outcome",
          eventId: 41,
          timestamp: "2026-07-23T12:00:00Z",
          taskUuid: "task-u-1",
          taskId: "T-00001",
          taskPath: "project/task",
          membership: "resident",
          campaignUuid: null,
          containerUuid: "p-1",
          outcome: { text: "done" },
        },
      ],
      snapshotEventId: 44,
      nextCursor: "snapshot-cursor",
    });
    const client = await clientWith(transport);

    const view = await client.wrkq.container.timelineView({
      container: "project",
      cursor: "previous-cursor",
      limit: 25,
    });

    expect(transport.capturedRequests[0]!.method).toBe("wrkq.container.timelineView");
    expect(transport.capturedRequests[0]!.params).toEqual({
      container: "project",
      cursor: "previous-cursor",
      limit: 25,
    });
    expect(view.campaign).toBeNull();
    expect(view.snapshotEventId).toBe(44);
    expect(view.entries[0]!.type).toBe("task.outcome");
    if (view.entries[0]!.type === "task.outcome") {
      expect(view.entries[0]!.outcome.text).toBe("done");
    }
  });

  test("container taskCounts forwards one aggregate request and returns typed rollups", async () => {
    const transport = new FakeTransport().onResult("wrkq.container.taskCounts", {
      items: [
        {
          uuid: "p-1",
          id: "P-00001",
          path: "project",
          kind: "project",
          projectUuid: "p-1",
          projectId: "P-00001",
          projectSlug: "project",
          totalTaskCount: 13,
          activeTaskCount: 5,
        },
      ],
    });
    const client = await clientWith(transport);

    const counts = await client.wrkq.container.taskCounts({ includeArchived: true });

    expect(transport.capturedRequests).toHaveLength(1);
    expect(transport.capturedRequests[0]).toMatchObject({
      method: "wrkq.container.taskCounts",
      params: { includeArchived: true },
    });
    expect(counts.items[0]?.projectUuid).toBe("p-1");
    expect(counts.items[0]?.totalTaskCount).toBe(13);
    expect(counts.items[0]?.activeTaskCount).toBe(5);
  });

  test("project root registry forwards listView and setRoot with verbatim root strings", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.project.listView", {
        items: [
          {
            type: "project",
            id: "P-00061",
            slug: "wrkq",
            title: "wrkq",
            path: "wrkq",
            root: "~/praesidium/wrkq",
          },
        ],
      })
      .onResult("wrkq.project.setRoot", {
        type: "project",
        id: "P-00061",
        slug: "wrkq",
        title: "wrkq",
        path: "wrkq",
        root: "/Volumes/work/wrkq",
      });
    const client = await clientWith(transport);

    const listed = await client.wrkq.project.listView({ includeArchived: true, limit: 10 });
    const updated = await client.wrkq.project.setRoot({
      project: "wrkq",
      root: "/Volumes/work/wrkq",
      expectEtag: 3,
      actor: "agent:larry",
    });

    expect(transport.capturedRequests[0]).toMatchObject({
      method: "wrkq.project.listView",
      params: { includeArchived: true, limit: 10 },
    });
    expect(transport.capturedRequests[1]).toMatchObject({
      method: "wrkq.project.setRoot",
      params: {
        project: "wrkq",
        root: "/Volumes/work/wrkq",
        expectEtag: 3,
        actor: "agent:larry",
      },
    });
    expect(listed.items[0]?.root).toBe("~/praesidium/wrkq");
    expect(updated.root).toBe("/Volumes/work/wrkq");
  });

  test("webhook facade sends add, remove and listView with params unchanged, listView defaulting to {}", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.webhook.add", { changed: true, count: 1, target: "https://hook.test/wrkq", webhook_urls: [] })
      .onResult("wrkq.webhook.remove", { changed: false, webhook_urls: [] })
      .onResult("wrkq.webhook.listView", []);
    const client = await clientWith(transport);

    const add = { url: "https://hook.test/wrkq", actor: "agent:larry" } satisfies WrkqWebhookMutateParams;
    const remove = { url: "https://absent.test/wrkq" } satisfies WrkqWebhookMutateParams;
    const added = await client.wrkq.webhook.add(add);
    await client.wrkq.webhook.remove(remove);
    await client.wrkq.webhook.listView();

    // Type-only: the mutation result exposes changed/count/target/webhook_urls.
    void ([added.changed, added.count, added.target, added.webhook_urls] satisfies [
      boolean,
      number | undefined,
      string | undefined,
      WrkqWebhookSubscription[],
    ]);
    expect(transport.capturedRequests.map(({ method, params }) => ({ method, params }))).toEqual([
      { method: "wrkq.webhook.add", params: add },
      { method: "wrkq.webhook.remove", params: remove },
      { method: "wrkq.webhook.listView", params: {} },
    ]);
  });
});
