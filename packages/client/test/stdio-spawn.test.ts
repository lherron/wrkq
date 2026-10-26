import { describe, expect, test } from "bun:test";
import { buildStdioSpawnSpec } from "../src/stdio-transport";
import type { StdioSpawnOptions } from "../src/stdio-transport";
import { createClient, type CreateClientOptions } from "../src/client";

describe("StdioTransport spawn construction", () => {
  test("T-05381 wrkq sessions use principalRef as --principal-ref caller attribution", () => {
    const spec = buildStdioSpawnSpec({
      command: "wrkq",
      dbPath: "/tmp/wrkq.db",
      principalRef: "agent:cody",
      env: { WRKQ_DB: undefined, WRKQ_DB_PATH: undefined },
    } as StdioSpawnOptions & { principalRef: string });

    expect(spec.argv).toEqual([
      "--db",
      "/tmp/wrkq.db",
      "--principal-ref",
      "agent:cody",
      "rpc",
      "--stdio",
    ]);
    expect(spec.argv).not.toContain("--as");
    expect(spec.env.WRKQ_DB).toBeUndefined();
  });

  test("createClient refuses a legacy actor option before spawning", async () => {
    await expect(
      createClient({ command: "wrkf", actor: "agent:cody", autoInitialize: false } as CreateClientOptions & {
        actor: string;
      }),
    ).rejects.toThrow("actor is no longer accepted for caller authority; use principalRef");
  });

  test("remote dbLocator uses WRKQ_DB and never --db", () => {
    const spec = buildStdioSpawnSpec({
      command: "wrkq",
      dbLocator: "rpc://127.0.0.1:7171",
      principalRef: "agent:cody",
      env: {
        WRKQ_DB: "rpc://old:7171",
        WRKQ_DB_PATH: "rpc://poison:7171",
        WRKQ_DB_PATH_FILE: "/tmp/poison-file",
      },
    });

    expect(spec.argv).toEqual(["--principal-ref", "agent:cody", "rpc", "--stdio"]);
    expect(spec.argv).not.toContain("--db");
    expect(spec.env.WRKQ_DB).toBe("rpc://127.0.0.1:7171");
    expect(spec.env.WRKQ_DB_PATH).toBeUndefined();
    expect(spec.env.WRKQ_DB_PATH_FILE).toBeUndefined();
  });

  test("createClient rejects a dbLocator that conflicts with dbPath before spawning", async () => {
    await expect(
      createClient({ command: "wrkq", dbLocator: "rpc://max3:7171", dbPath: "/tmp/wrkq.db", autoInitialize: false }),
    ).rejects.toThrow("dbLocator and dbPath refer to different database locators");
  });

  test("wrkf local path uses canonical --principal-ref and wrkf-only flags", () => {
    const spec = buildStdioSpawnSpec({
      command: "wrkf",
      dbLocator: "/tmp/wrkq.db",
      principalRef: "agent:cody",
      role: "coordinator",
      hookCatalogPath: "/tmp/hooks.json",
    });

    expect(spec.argv).toEqual([
      "--db",
      "/tmp/wrkq.db",
      "--principal-ref",
      "agent:cody",
      "--role",
      "coordinator",
      "--hook-catalog",
      "/tmp/hooks.json",
      "rpc",
      "--stdio",
    ]);
    expect(spec.argv).not.toContain("--actor");
  });

  test("createClient refuses a caller hook catalog for a remote wrkf locator before spawning", async () => {
    await expect(
      createClient({
        command: "wrkf",
        dbLocator: "rpc://mini:7171",
        hookCatalogPath: "/caller/hooks.json",
        autoInitialize: false,
      }),
    ).rejects.toThrow("hookCatalogPath is local-only; hook catalog is canonical-node configuration in remote mode");
  });
});
