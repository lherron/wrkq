/**
 * remote-locator.integration.test.ts — proves @wrkq/client can use a rpc:// DB
 * locator through the stdio subprocess bridge without a local DB.
 *
 * Requires installed wrkq, wrkqd, and wrkqadm on PATH. `just verify` runs this
 * after `just install`.
 */

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer } from "node:net";
import { createClient, type WorkClient } from "../src/client";

const WRKQ = process.env.WRKQ_BIN ?? "wrkq";
const WRKQD = process.env.WRKQD_BIN ?? "wrkqd";
const WRKQADM = process.env.WRKQADM_BIN ?? "wrkqadm";

let dir: string;
let dbPath: string;
let attachDir: string;
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
  port = await freePort();
  token = `remote-client-${process.pid}-${Date.now()}`;

  execFileSync(WRKQADM, ["init", "--db", dbPath, "--attach-dir", attachDir], {
    cwd: dir,
    env: { ...process.env, ASP_PROJECT: "", WRKQ_PROJECT: "" },
    stdio: "ignore",
  });

  daemon = Bun.spawn([WRKQD, "--db", dbPath, "--addr", `127.0.0.1:${port}`, "--token", token], {
    cwd: dir,
    env: { ...process.env, WRKQ_ATTACH_DIR: attachDir },
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
        actor: "agent:local-human",
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

      const workflows = await client.wrkf.workflow.list();
      expect(Array.isArray(workflows.templates)).toBe(true);
    } finally {
      rmSync(cwd, { recursive: true, force: true });
    }
  });
});
