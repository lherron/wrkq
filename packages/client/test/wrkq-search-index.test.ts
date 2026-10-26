/**
 * wrkq-search-index.test.ts — wrkq search + index family (T-05114).
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { FakeTransport } from "../src/testing/fake-transport";
import { clientWith } from "./support/client-fixtures";

describe("wrkq search + index family (T-05114)", () => {
  const MOCK_STATUS = {
    path: "/db/wrkq.db.search.sqlite",
    enabled: true,
    status: "ready",
    last_indexed_event_id: 5,
    canonical_max_event_id: 5,
    stale_event_count: 0,
    searchable_chunk_count: 3,
  };

  test("search.listView sends wrkq.search.listView and returns the legacy response shape", async () => {
    const MOCK_VIEW = {
      query: "alpha",
      stale: false,
      status: MOCK_STATUS,
      results: [
        {
          resource_type: "task",
          resource_id: "T-00001",
          resource_uuid: "u-1",
          task_id: "T-00001",
          task_uuid: "u-1",
          path: "inbox/find-me",
          title: "Searchable Task",
          state: "open",
          kind: "task",
          snippet: "alpha beta gamma",
          score: 0.0164,
          created_at: "2026-06-23T00:00:00Z",
          updated_at: "2026-06-23T00:00:00Z",
          stale: false,
        },
      ],
      total_matches: 1,
      offset: 0,
    };
    const transport = new FakeTransport().onResult("wrkq.search.listView", MOCK_VIEW);
    const client = await clientWith(transport);

    const view = await client.wrkq.search.listView({
      query: "alpha",
      paths: ["inbox"],
      state: "all",
      labels: ["alpha", "beta"],
      sort: "updated_at",
      fresh: true,
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.search.listView");
    expect(frame.params).toMatchObject({
      query: "alpha",
      paths: ["inbox"],
      state: "all",
      labels: ["alpha", "beta"],
      sort: "updated_at",
      fresh: true,
    });
    // Result keys are DELIBERATELY snake_case (legacy search.Response byte-parity).
    expect(view.total_matches).toBe(1);
    expect(view.results[0]!.resource_id).toBe("T-00001");
    expect(view.status!.searchable_chunk_count).toBe(3);
  });

  test("index facade sends each lifecycle method with {} or the caller's params", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.index.status", MOCK_STATUS)
      .onResult("wrkq.index.rebuild", { rebuilt: true, status: MOCK_STATUS })
      .onResult("wrkq.index.update", { updated: true, status: MOCK_STATUS })
      .onResult("wrkq.index.vacuum", { vacuumed: true })
      .onResult("wrkq.index.pause", { status: "paused" })
      .onResult("wrkq.index.resume", { status: "ready" });
    const client = await clientWith(transport);

    await client.wrkq.index.status();
    await client.wrkq.index.rebuild({ foreground: true });
    await client.wrkq.index.update();
    await client.wrkq.index.vacuum();
    await client.wrkq.index.pause();
    await client.wrkq.index.resume();

    expect(transport.capturedRequests.map(({ method, params }) => ({ method, params }))).toEqual([
      { method: "wrkq.index.status", params: {} },
      { method: "wrkq.index.rebuild", params: { foreground: true } },
      { method: "wrkq.index.update", params: {} },
      { method: "wrkq.index.vacuum", params: {} },
      { method: "wrkq.index.pause", params: {} },
      { method: "wrkq.index.resume", params: {} },
    ]);
  });
});
