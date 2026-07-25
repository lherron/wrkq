/**
 * remote-locator.integration.test.ts — proves @wrkq/client can use a rpc:// DB
 * locator through the stdio subprocess bridge without a local DB.
 *
 * Requires installed wrkq, wrkqd, and wrkqadm on PATH. `just verify` runs this
 * after `just install`.
 */

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer } from "node:net";
import { createClient, type WorkClient } from "../src/client";
import { WorkRpcError } from "../src/errors";

const WRKQ = process.env.WRKQ_BIN ?? "wrkq";
const WRKQD = process.env.WRKQD_BIN ?? "wrkqd";
const WRKQADM = process.env.WRKQADM_BIN ?? "wrkqadm";

let dir: string;
let dbPath: string;
let attachDir: string;
let hookCatalogPath: string;
let port: number;
let token: string;
let daemon: ReturnType<typeof Bun.spawn> | undefined;

async function freePort(): Promise<number> {
  return await new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (address == null || typeof address === "string") {
        server.close(() => reject(new Error("failed to allocate TCP port")));
        return;
      }
      const p = address.port;
      server.close(() => resolve(p));
    });
  });
}

async function waitForHealth(): Promise<void> {
  const url = `http://127.0.0.1:${port}/v1/health`;
  let last = "";
  for (let i = 0; i < 100; i++) {
    try {
      const res = await fetch(url, { headers: { Authorization: `Bearer ${token}` } });
      if (res.ok) return;
      last = `${res.status} ${await res.text()}`;
    } catch (err) {
      last = (err as Error).message;
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`wrkqd did not become healthy: ${last}`);
}

beforeAll(async () => {
  dir = mkdtempSync(join(tmpdir(), "wrkq-client-remote-"));
  dbPath = join(dir, "wrkq.db");
  attachDir = join(dir, "attachments");
  hookCatalogPath = join(dir, "empty-hook-catalog.json");
  writeFileSync(hookCatalogPath, JSON.stringify({ schemaVersion: "wrkf.hook-catalog.v0", hooks: {} }));
  port = await freePort();
  token = `remote-client-${process.pid}-${Date.now()}`;

  execFileSync(WRKQADM, ["init", "--db", dbPath, "--attach-dir", attachDir], {
    cwd: dir,
    env: { ...process.env, ASP_PROJECT: "", WRKQ_PROJECT: "" },
    stdio: "ignore",
  });

  // Principal-only attribution: the remote wrkqd's /v1/rpc surface attributes
  // writes to its launch-time service principal (per-request principal forwarding
  // over the remote transport is a separate, larger feature). Give it the same
  // agent identity the client declares below so the forwarded write is attributed.
  daemon = Bun.spawn([WRKQD, "--db", dbPath, "--addr", `127.0.0.1:${port}`, "--token", token], {
    cwd: dir,
    env: {
      ...process.env,
      WRKF_HOOK_CATALOG: hookCatalogPath,
      WRKQ_ATTACH_DIR: attachDir,
      WRKQ_PRINCIPAL_REF: "agent:local-human",
    },
    stdout: "pipe",
    stderr: "pipe",
  });
  await waitForHealth();
});

afterAll(() => {
  daemon?.kill();
  if (dir) rmSync(dir, { recursive: true, force: true });
});

describe("@wrkq/client remote locator through stdio subprocess", () => {
  let client: WorkClient | undefined;

  afterAll(async () => {
    if (client) await client.close();
  });

  test("legacy dbPath accepts rpc:// locator and reaches remote wrkqd", async () => {
    const cwd = mkdtempSync(join(tmpdir(), "wrkq-client-remote-cwd-"));
    try {
      client = await createClient({
        command: WRKQ,
        dbPath: `rpc://127.0.0.1:${port}`,
        principalRef: "agent:local-human",
        cwd,
        env: {
          ...process.env,
          WRKQD_TOKEN: token,
          WRKQ_DB_PATH: "rpc://poison:7171",
          WRKQ_DB_PATH_FILE: "/tmp/poison-token-file",
          ASP_PROJECT: "",
          WRKQ_PROJECT: "",
        },
      });

      const task = await client.wrkq.task.create({
        title: "remote locator client task",
        state: "open",
        kind: "task",
        project: "inbox",
        idempotencyKey: "remote-locator:create",
      });
      expect(task.id).toMatch(/^T-/);

      const shown = await client.wrkq.task.show({ task: task.id });
      expect(shown.title).toBe("remote locator client task");

      const remoteCounts = await client.wrkq.container.taskCounts();
      const localClient = await createClient({
        command: WRKQ,
        dbPath,
        principalRef: "agent:local-human",
        cwd,
        env: {
          ...process.env,
          ASP_PROJECT: "",
          WRKQ_PROJECT: "",
        },
      });
      try {
        const localCounts = await localClient.wrkq.container.taskCounts();
        expect(remoteCounts).toEqual(localCounts);
        const inbox = remoteCounts.items.find((item) => item.path === "inbox");
        expect(inbox?.totalTaskCount).toBe(1);
        expect(inbox?.activeTaskCount).toBe(1);
      } finally {
        await localClient.close();
      }

      let notFound: unknown;
      try {
        await client.wrkq.task.show({ task: "T-99999" });
      } catch (error) {
        notFound = error;
      }
      expect(notFound).toBeInstanceOf(WorkRpcError);
      expect((notFound as WorkRpcError).domainCode).toBe("WRKQ_NOT_FOUND");

      const workflows = await client.wrkf.workflow.list();
      expect(Array.isArray(workflows.templates)).toBe(true);
    } finally {
      rmSync(cwd, { recursive: true, force: true });
    }
  });

  test("bad remote credentials reject with an explicit protocol-level authentication error", async () => {
    const badToken = `invalid-secret-${process.pid}`;
    let caught: unknown;
    try {
      await createClient({
        command: WRKQ,
        dbPath: `rpc://127.0.0.1:${port}`,
        principalRef: "agent:local-human",
        env: {
          ...process.env,
          WRKQD_TOKEN: badToken,
          WRKQD_TOKEN_FILE: undefined,
          ASP_PROJECT: "",
          WRKQ_PROJECT: "",
        },
      });
    } catch (error) {
      caught = error;
    }

    expect(caught).toBeInstanceOf(WorkRpcError);
    const error = caught as WorkRpcError;
    expect(error.rpcCode).toBe(-32603);
    expect(error.domainCode).toBeUndefined();
    expect(error.message).toContain("authentication failed (HTTP 401)");
    expect(error.data?.kind).toBe("authentication");
    expect(error.data?.httpStatus).toBe(401);
    expect(error.message).not.toContain(badToken);
  });
});
