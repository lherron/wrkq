import { describe, expect, test } from "bun:test";
import { buildStdioSpawnSpec } from "../src/stdio-transport";

describe("StdioTransport spawn construction", () => {
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

  test("wrkf local path keeps wrkf-only flags", () => {
    const spec = buildStdioSpawnSpec({
      command: "wrkf",
      dbLocator: "/tmp/wrkq.db",
      actor: "agent:cody",
      role: "coordinator",
      hookCatalogPath: "/tmp/hooks.json",
    });

    expect(spec.argv).toEqual([
      "--db",
      "/tmp/wrkq.db",
      "--actor",
      "agent:cody",
      "--role",
      "coordinator",
      "--hook-catalog",
      "/tmp/hooks.json",
      "rpc",
      "--stdio",
    ]);
  });
});
