/**
 * action.integration.test.ts — low-ceremony wrkf.action.* flow against the REAL
 * `wrkq rpc --stdio` binary, through @wrkq/client only (T-05009).
 *
 *   initialize → task.create → action.start (built-in workflow)
 *   → action.bindExternal (hrc:<id>) → action.complete (evidence + transition)
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
  test("start (built-in) → bindExternal → complete → list, typed results", async () => {
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
      action: "triage",
      actor: ACTOR,
      idempotencyKey: "action-itest:start:1",
    });
    expect(typeof run.runId).toBe("string");
    expect(run.actionRunId).toBe(run.runId);
    expect(run.action).toBe("triage");
    expect(run.role).toBe("triager");
    expect(run.status).toBe("active");
    expect(run.workflow.id).toBe("wrkq-simple-task");

    // Idempotent replay → same run.
    const again = await client.wrkf.action.start({
      task: task.id,
      action: "triage",
      actor: ACTOR,
      idempotencyKey: "action-itest:start:1",
    });
    expect(again.runId).toBe(run.runId);

    // ── action.bindExternal — hrc:<id> ──
    const bound = await client.wrkf.action.bindExternal({
      actionRunId: run.runId,
      externalRunRef: "hrc:itest-run-1",
    });
    expect(bound.externalRunRef).toBe("hrc:itest-run-1");

    // ── action.complete — evidence + default triage_complete transition ──
    const completed = await client.wrkf.action.complete({
      actionRunId: run.runId,
      evidence: { summary: "triaged" },
      runSummary: "done",
    });
    expect(completed.run.status).toBe("completed");
    expect(completed.evidence?.kind).toBe("triage_result");
    expect(completed.evidence?.runId).toBe(run.runId);
    expect(completed.transition?.state).toBeDefined();

    // ── action.list — all runs for the task ──
    const list = await client.wrkf.action.list({
      task: task.id,
      includeClosedInstances: true,
    });
    expect(list.items.length).toBe(1);
    expect(list.items[0]!.runId).toBe(run.runId);
    expect(list.items[0]!.externalRunRef).toBe("hrc:itest-run-1");
    expect(list.items[0]!.status).toBe("completed");
  });
});
