import { describe, expect, test } from "bun:test";
import { buildStdioSpawnSpec } from "../src/stdio-transport";
import type { StdioSpawnOptions } from "../src/stdio-transport";

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

  test("wrkq sessions reject legacy actor; wrkf launches with canonical --principal-ref", () => {
    let wrkqRejectedActor = false;
    try {
      buildStdioSpawnSpec({
        command: "wrkq",
        actor: "agent:cody",
      });
    } catch (err) {
      wrkqRejectedActor = true;
      expect((err as Error).message).toMatch(/actor|principalRef/i);
    }
    expect(wrkqRejectedActor).toBe(true);

    // T-05372: wrkf participant identity is canonical principal_ref — the wrkf
    // binary launches with --principal-ref, never the legacy --actor flag.
    const wrkfSpec = buildStdioSpawnSpec({
      command: "wrkf",
      principalRef: "agent:cody",
    });
    expect(wrkfSpec.argv).toEqual(["--principal-ref", "agent:cody", "rpc", "--stdio"]);
    expect(wrkfSpec.argv).not.toContain("--actor");
  });

  test("local dbPath stays on path-only --db", () => {
    const spec = buildStdioSpawnSpec({
      command: "wrkq",
      dbPath: "/tmp/wrkq.db",
      actor: "agent:cody",
      env: { WRKQ_DB: undefined, WRKQ_DB_PATH: undefined },
    });

    expect(spec.argv).toEqual(["--db", "/tmp/wrkq.db", "--as", "agent:cody", "rpc", "--stdio"]);
    expect(spec.env.WRKQ_DB).toBeUndefined();
  });

  test("remote dbLocator uses WRKQ_DB and never --db", () => {
    const spec = buildStdioSpawnSpec({
      command: "wrkq",
      dbLocator: "rpc://127.0.0.1:7171",
      actor: "agent:cody",
      env: {
        WRKQ_DB: "rpc://old:7171",
        WRKQ_DB_PATH: "rpc://poison:7171",
        WRKQ_DB_PATH_FILE: "/tmp/poison-file",
      },
    });

    expect(spec.argv).toEqual(["--as", "agent:cody", "rpc", "--stdio"]);
    expect(spec.argv).not.toContain("--db");
    expect(spec.env.WRKQ_DB).toBe("rpc://127.0.0.1:7171");
    expect(spec.env.WRKQ_DB_PATH).toBeUndefined();
    expect(spec.env.WRKQ_DB_PATH_FILE).toBeUndefined();
  });

  test("legacy dbPath may carry a remote locator", () => {
    const spec = buildStdioSpawnSpec({
      command: "wrkq",
      dbPath: "rpc://max3:7171",
      env: { WRKQ_DB_PATH: "rpc://poison:7171" },
    });

    expect(spec.argv).toEqual(["rpc", "--stdio"]);
    expect(spec.env.WRKQ_DB).toBe("rpc://max3:7171");
    expect(spec.env.WRKQ_DB_PATH).toBeUndefined();
  });

  test("dbLocator and dbPath conflict is rejected", () => {
    expect(() =>
      buildStdioSpawnSpec({
        command: "wrkq",
        dbLocator: "rpc://max3:7171",
        dbPath: "/tmp/wrkq.db",
      }),
    ).toThrow("dbLocator and dbPath refer to different database locators");
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
});
