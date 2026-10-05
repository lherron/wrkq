/**
 * client-lifecycle.test.ts — createClient lifecycle: initialize handshake and shutdown.
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { createClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";
import { clientWith } from "./support/client-fixtures";

describe("createClient lifecycle", () => {
  test("autoInitialize sends rpc.initialize with the protocol version", async () => {
    const transport = new FakeTransport().onResult("rpc.initialize", {
      protocolVersion: "2026-06-30",
      protocolSchemaHash: "sha256:x",
      server: {
        name: "wrkq-wrkf-rpc",
        version: "dev",
        revision: "unknown",
        pid: 1,
        entrypoint: "wrkq",
      },
      database: { path: "/db", migrationHash: "sha256:y" },
      capabilities: {
        cancel: true,
        wrkq: true,
        wrkf: true,
        effectClaimLease: true,
        runExternalBinding: true,
      },
      methods: ["rpc.initialize"],
    });

    const client = await createClient({ transport, autoInitialize: true });
    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("rpc.initialize");
    expect(frame.jsonrpc).toBe("2.0");
    expect(frame.params).toMatchObject({ protocolVersion: "2026-06-30" });
    await client.close();
    expect(transport.closed).toBe(true);
  });

  test("close() sends rpc.shutdown then closes transport", async () => {
    const transport = new FakeTransport().onResult("rpc.shutdown", {});
    const client = await clientWith(transport);
    await client.close();
    expect(transport.capturedRequests.some((r) => r.method === "rpc.shutdown")).toBe(true);
    expect(transport.closed).toBe(true);
  });
});
