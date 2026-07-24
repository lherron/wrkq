/**
 * action.integration.test.ts — low-ceremony wrkf.action.* flow against the REAL
 * `wrkq rpc --stdio` binary, through @wrkq/client only (T-05009).
 *
 *   initialize → task.create → action.start (built-in workflow)
 *   → action.bindExternal (hrc:<id>) → action.fail (terminal run)
 *   → action.list (includeClosedInstances)
 *
 * Binary discovery: env WRKQ_BIN / WRKQADM_BIN, else PATH.
 * Requires `just install` (wrkq, wrkqadm on PATH) to have run.
 */

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createClient, type WorkClient } from "../src/client";

delete process.env.ASP_PROJECT;
delete process.env.WRKQ_PROJECT;

const WRKQ = process.env.WRKQ_BIN ?? "wrkq";
const WRKQADM = process.env.WRKQADM_BIN ?? "wrkqadm";
const ACTOR = "agent:action-itest";

let dir: string;
let dbPath: string;
let client: WorkClient;

beforeAll(() => {
  dir = mkdtempSync(join(tmpdir(), "wrkq-client-action-"));
  dbPath = join(dir, "wrkq.db");
  const env = { ...process.env };
  delete env.ASP_PROJECT;
  delete env.WRKQ_PROJECT;
  execFileSync(WRKQADM, ["init", "--db", dbPath], { env, cwd: dir, stdio: "ignore" });
});

afterAll(async () => {
  try {
    if (client) await client.close();
  } finally {
    if (dir) rmSync(dir, { recursive: true, force: true });
  }
});

describe("wrkf.action.* via @wrkq/client over real `wrkq rpc --stdio`", () => {
  test("start (default @5) → bindExternal → fail → list, typed results", async () => {
    client = await createClient({
      command: WRKQ,
      dbPath,
      actor: ACTOR,
      cwd: dir,
      env: { ...process.env, ASP_PROJECT: "", WRKQ_PROJECT: "" },
    });

    const task = await client.wrkq.task.create({
      title: "action-itest task",
      kind: "task",
      state: "open",
    });
    expect(task.id).toBe("T-00001");

    // ── action.start — no workflow supplied → built-in wrkq-simple-task ──
    const run = await client.wrkf.action.start({
      task: task.id,
      action: "implement",
      principal_ref: ACTOR,
      idempotencyKey: "action-itest:start:1",
    });
    expect(typeof run.runId).toBe("string");
    expect(run.actionRunId).toBe(run.runId);
    expect(run.action).toBe("implement");
    expect(run.role).toBe("implementer");
    expect(run.status).toBe("active");
    expect(run.workflow.id).toBe("wrkq-simple-task");
    expect(run.workflow.version).toBe("5");

    // Idempotent replay → same run.
    const again = await client.wrkf.action.start({
      task: task.id,
      action: "implement",
      principal_ref: ACTOR,
      idempotencyKey: "action-itest:start:1",
    });
    expect(again.runId).toBe(run.runId);

    // ── evidence.list — instance-only selection, evidence-free instance (T-06324) ──
    const inst = await client.wrkf.instance.show({ task: task.id });
    const emptyEvidence = await client.wrkf.evidence.list({ instanceId: inst.id });
    expect(Array.isArray(emptyEvidence)).toBe(true);
    expect(emptyEvidence.length).toBe(0);

    // ── action.bindExternal — hrc:<id> ──
    const bound = await client.wrkf.action.bindExternal({
      actionRunId: run.runId,
      externalRunRef: "hrc:itest-run-1",
    });
    expect(bound.externalRunRef).toBe("hrc:itest-run-1");

    // ── action.fail — terminal run without changing the attached workflow ──
    const failed = await client.wrkf.action.fail({
      actionRunId: run.runId,
      summary: "expected integration terminalization",
      evidence: { summary: "typed failure evidence" },
    });
    expect(failed.status).toBe("failed");

    // ── action.list — all runs for the task ──
    const list = await client.wrkf.action.list({
      task: task.id,
      includeClosedInstances: true,
    });
    expect(list.items.length).toBe(1);
    expect(list.items[0]!.runId).toBe(run.runId);
    expect(list.items[0]!.externalRunRef).toBe("hrc:itest-run-1");
    expect(list.items[0]!.status).toBe("failed");
  });
});
