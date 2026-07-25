/**
 * rpc-entrypoint-equivalence.integration.test.ts — proves `wrkq rpc --stdio`
 * and `wrkf rpc --stdio` expose the identical facade (spec §5.2, contract §3).
 *
 * Compares protocolVersion, protocolSchemaHash, capabilities, and the full
 * method catalog. The only permitted difference is the diagnostic
 * `server.entrypoint` field. Also re-runs a minimal business flow through the
 * `wrkf` entrypoint to confirm behavior parity.
 *
 * Binary discovery: env WRKQ_BIN / WRKF_BIN / WRKQADM_BIN, else PATH.
 */

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createClient, type WorkClient } from "../src/client";
import { PROTOCOL_VERSION } from "../src/protocol";

delete process.env.ASP_PROJECT;
delete process.env.WRKQ_PROJECT;

const WRKQ = process.env.WRKQ_BIN ?? "wrkq";
const WRKF = process.env.WRKF_BIN ?? "wrkf";
const WRKQADM = process.env.WRKQADM_BIN ?? "wrkqadm";

let dir: string;
let dbPath: string;

function mkdb(): string {
  const env = { ...process.env };
  delete env.ASP_PROJECT;
  delete env.WRKQ_PROJECT;
  const d = mkdtempSync(join(tmpdir(), "wrkq-equiv-"));
  const db = join(d, "wrkq.db");
  execFileSync(WRKQADM, ["init", "--db", db], { env, cwd: d, stdio: "ignore" });
  return db;
}

beforeAll(() => {
  dir = mkdtempSync(join(tmpdir(), "wrkq-equiv-root-"));
  dbPath = mkdb();
});

afterAll(() => {
  if (dir) rmSync(dir, { recursive: true, force: true });
});

async function initVia(command: string, db: string) {
  const client = await createClient({
    command,
    dbPath: db,
    principalRef: "agent:local-human",
    cwd: tmpdir(),
    autoInitialize: false,
    env: { ...process.env, ASP_PROJECT: "", WRKQ_PROJECT: "" },
  });
  const init = await client.rpc.initialize();
  return { client, init };
}

describe("entrypoint equivalence", () => {
  let wrkqClient: WorkClient | undefined;
  let wrkfClient: WorkClient | undefined;

  afterAll(async () => {
    if (wrkqClient) await wrkqClient.close();
    if (wrkfClient) await wrkfClient.close();
  });

  test("wrkq and wrkf rpc expose identical protocol facade", async () => {
    const a = await initVia(WRKQ, dbPath);
    const b = await initVia(WRKF, mkdb());
    wrkqClient = a.client;
    wrkfClient = b.client;

    expect(a.init.protocolVersion).toBe(PROTOCOL_VERSION);
    expect(b.init.protocolVersion).toBe(PROTOCOL_VERSION);

    // Same protocol schema hash across entrypoints.
    expect(a.init.protocolSchemaHash).toBe(b.init.protocolSchemaHash);

    // Same capabilities.
    expect(b.init.capabilities).toEqual(a.init.capabilities);

    // Same method catalog (order-independent).
    expect([...b.init.methods].sort()).toEqual([...a.init.methods].sort());

    // Only the diagnostic entrypoint differs.
    expect(a.init.server.entrypoint).toBe("wrkq");
    expect(b.init.server.entrypoint).toBe("wrkf");
    expect(a.init.server.name).toBe(b.init.server.name);

    // Catalog includes required methods and excludes forbidden ones.
    const methods = new Set(a.init.methods);
    for (const m of [
      "rpc.initialize",
      "wrkq.task.create",
      "wrkq.workflow.attach",
      "wrkf.workflow.install",
      "wrkf.transition.apply",
      "wrkf.instance.show",
      "wrkf.instance.next",
      "wrkf.event.query",
      "wrkf.role.list",
      "wrkf.role.bind",
      "wrkf.role.unbind",
      "wrkf.role.set",
    ]) {
      expect(methods.has(m)).toBe(true);
    }
    for (const m of [
      "wrkf.task.attach",
      "wrkf.task.inspect",
      "wrkf.task.timeline",
      "wrkf.task.refresh",
      "wrkf.task.syncMeta",
      "wrkf.workflow.attach",
      "wrkf.initialize",
      "wrkq.actor.list",
      "wrkq.actor.create",
      "wrkq.actor.update",
    ]) {
      expect(methods.has(m)).toBe(false);
    }
  });

  test("task.create behaves the same through the wrkf entrypoint", async () => {
    const task = await wrkfClient!.wrkq.task.create({
      title: "via wrkf entrypoint",
      kind: "task",
      state: "open",
    });
    expect(task.id).toBe("T-00001");
    expect(task.state).toBe("open");
  });
});

// ── initialize-shape (daedalus acceptance) ────────────────────────────────────
//
// Asserts the rpc.initialize response layout (spec §3, contract §4):
//   - protocolSchemaHash is a top-level non-empty string
//   - database.migrationHash exists under database{}
//   - NO legacy top-level schemaHash field
//
describe("initialize result shape", () => {
  test("protocolSchemaHash top-level, database.migrationHash nested, no legacy top-level schemaHash", async () => {
    const client = await createClient({
      command: WRKQ,
      dbPath: mkdb(),
      principalRef: "agent:local-human",
      cwd: tmpdir(),
      autoInitialize: false,
      env: { ...process.env, ASP_PROJECT: "", WRKQ_PROJECT: "" },
    });
    const init = await client.rpc.initialize();
    await client.close().catch(() => {});

    // protocolSchemaHash must be a top-level non-empty string.
    expect(typeof init.protocolSchemaHash).toBe("string");
    expect(init.protocolSchemaHash.length).toBeGreaterThan(0);

    // migrationHash must live under database{}, not at top level.
    expect(typeof init.database?.migrationHash).toBe("string");
    expect(init.database.migrationHash.length).toBeGreaterThan(0);

    // No legacy top-level schemaHash field (spec §3).
    const raw = init as unknown as Record<string, unknown>;
    expect(raw["schemaHash"]).toBeUndefined();
  });
});
