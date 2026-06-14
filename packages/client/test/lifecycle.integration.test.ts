/**
 * lifecycle.integration.test.ts — agent-loop-ready flow against the REAL
 * `wrkq rpc --stdio` binary (spec §14), through @wrkq/client only.
 *
 *   initialize → task.create → workflow.install → workflow.attach
 *   → run.start → evidence.add (with runId) → workflow.inspect
 *   → transition.apply (CAS stale error path, then commit)
 *   → effect.list/claim/ack → run.finish → close
 *
 * No wrkqd HTTP, no CLI-output parsing, no root client.* business namespace.
 *
 * Binary discovery: env WRKQ_BIN / WRKQADM_BIN, else PATH.
 * Requires `just install` (wrkq, wrkqadm on PATH) to have run.
 */

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createClient, type WorkClient } from "../src/client";
import { WorkRpcError, isWrkfError } from "../src/errors";

delete process.env.ASP_PROJECT;
delete process.env.WRKQ_PROJECT;

const WRKQ = process.env.WRKQ_BIN ?? "wrkq";
const WRKF = process.env.WRKF_BIN ?? "wrkf";
const WRKQADM = process.env.WRKQADM_BIN ?? "wrkqadm";
const ACTOR = "human:local-human";

const WORKFLOW = {
  schemaVersion: "wrkf.workflow-template.v0",
  id: "itest_flow",
  version: "1",
  kind: "agent_first_workflow",
  initial: { status: "active", phase: "plan" },
  roles: { coordinator: { description: "Coordinates" } },
  states: [
    { status: "active", phase: "plan" },
    { status: "active", phase: "done" },
    { status: "closed", outcome: "completed" },
  ],
  evidenceKinds: {
    implementation: {
      description: "Implementation proof",
      facts: {
        required: ["verdict"],
        properties: { verdict: { type: "string", enum: ["ready", "needs_patch"] } },
      },
    },
  },
  obligationKinds: {},
  checks: {},
  transitions: [
    {
      id: "plan_ready",
      from: { status: "active", phase: "plan" },
      by: ["coordinator"],
      requires: [{ evidence: { kind: "implementation", facts: { verdict: "ready" } } }],
      outcomes: [
        {
          id: "ready",
          when: { always: true },
          to: { status: "active", phase: "done" },
          effects: [{ kind: "wake_role", role: "coordinator", reason: "ready to finish" }],
        },
      ],
    },
  ],
};

let dir: string;
let dbPath: string;
let workflowPath: string;
let client: WorkClient;

beforeAll(() => {
  dir = mkdtempSync(join(tmpdir(), "wrkq-client-itest-"));
  dbPath = join(dir, "wrkq.db");
  workflowPath = join(dir, "workflow.json");
  writeFileSync(workflowPath, JSON.stringify(WORKFLOW));

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

describe("WorkClient over real `wrkq rpc --stdio`", () => {
  test("agent-loop-ready lifecycle with CAS, evidence runId, runs, effects", async () => {
    client = await createClient({
      command: WRKQ,
      dbPath,
      actor: ACTOR,
      role: "coordinator",
      cwd: dir,
      env: { ...process.env, ASP_PROJECT: "", WRKQ_PROJECT: "" },
    });

    // createClient auto-initialized; assert the handshake side effects via a probe.
    const installed = await client.wrkf.workflow.install({ path: workflowPath });
    expect(installed.id).toBe("itest_flow");
    expect(installed.installed).toBe(true);

    // ── wrkq.task.create ──
    const task = await client.wrkq.task.create({
      title: "agent-loop task",
      kind: "task",
      state: "open",
      idempotencyKey: "itest:create",
    });
    expect(task.id).toBe("T-00001");
    expect(task.state).toBe("open");

    // idempotent replay → same task
    const again = await client.wrkq.task.create({
      title: "agent-loop task",
      kind: "task",
      state: "open",
      idempotencyKey: "itest:create",
    });
    expect(again.id).toBe(task.id);

    // ── wrkq.workflow.attach (a wrkq verb) ──
    const attached = await client.wrkq.workflow.attach({
      task: task.id,
      workflow: "itest_flow@1",
      actor: "agent:agent-loop",
      idempotencyKey: `itest:attach:${task.id}`,
    });
    expect(attached.attached).toBe(true);
    expect(attached.instance.revision).toBe(0);
    expect(attached.instance.phase).toBe("plan");

    // ── wrkf.run.start ──
    const run = await client.wrkf.run.start({
      task: task.id,
      role: "coordinator",
      actor: "agent:agent-loop",
      externalRunRef: "agent-loop-run-1",
      idempotencyKey: `itest:run:${task.id}:1`,
    });
    expect(typeof run.id).toBe("string");

    // ── wrkf.evidence.add (needs_patch, then ready) — ready binds runId ──
    const needsPatch = await client.wrkf.evidence.add({
      task: task.id,
      kind: "implementation",
      ref: "git:abc",
      summary: "needs patch",
      facts: { verdict: "needs_patch" },
      role: "coordinator",
      actor: "agent:agent-loop",
    });
    expect(needsPatch.facts?.verdict).toBe("needs_patch");

    const staleHash = (await client.wrkq.workflow.inspect({ task: task.id })).instance.contextHash;

    const ready = await client.wrkf.evidence.add({
      task: task.id,
      kind: "implementation",
      ref: "git:def",
      summary: "ready",
      facts: { verdict: "ready" },
      role: "coordinator",
      actor: "agent:agent-loop",
      runId: run.id,
    });
    expect(ready.facts?.verdict).toBe("ready");
    expect(ready.runId).toBe(run.id);

    // ── CAS stale error path: apply with stale contextHash → typed WorkRpcError ──
    let casErr: unknown;
    try {
      await client.wrkf.transition.apply({
        task: task.id,
        transition: "plan_ready",
        role: "coordinator",
        actor: "agent:agent-loop",
        expectRevision: 0,
        contextHash: staleHash,
      });
    } catch (e) {
      casErr = e;
    }
    expect(casErr).toBeInstanceOf(WorkRpcError);
    expect(isWrkfError(casErr)).toBe(true);
    expect((casErr as WorkRpcError).retryable).toBe(true);

    // ── wrkf.transition.apply (commit) with fresh CAS ──
    const fresh = await client.wrkq.workflow.inspect({ task: task.id });
    const transitioned = await client.wrkf.transition.apply({
      task: task.id,
      transition: "plan_ready",
      role: "coordinator",
      actor: "agent:agent-loop",
      expectRevision: fresh.instance.revision,
      contextHash: fresh.instance.contextHash,
      idempotencyKey: `itest:transition:${task.id}:plan_ready:${fresh.instance.revision}`,
    });
    expect(transitioned.instanceId).toBe(attached.instance.id);
    expect(transitioned.revision).toBe(1);
    expect(transitioned.effects.length).toBe(1);
    expect(transitioned.effects[0]!.kind).toBe("wake_role");

    // ── wrkf.effect.list / claim / ack with lease token ──
    const effects = await client.wrkf.effect.list({ task: task.id, all: true });
    expect(effects.length).toBe(1);

    const claim = await client.wrkf.effect.claim({ adapter: "wake_role", limit: 5, leaseMs: 60000 });
    expect(claim.effects.length).toBe(1);
    expect(typeof claim.leaseToken).toBe("string");
    const effId = claim.effects[0]!.id;

    const acked = await client.wrkf.effect.ack({ effectId: effId, leaseToken: claim.leaseToken });
    expect(acked.id).toBe(effId);

    // ── wrong lease token → WRKF_LEASE_CONFLICT (typed) ──
    let leaseErr: unknown;
    try {
      await client.wrkf.effect.fail({
        effectId: effId,
        leaseToken: "lease_bogus",
        reason: "nope",
      });
    } catch (e) {
      leaseErr = e;
    }
    expect(isWrkfError(leaseErr)).toBe(true);

    // ── wrkf.run.finish ──
    const finished = await client.wrkf.run.finish({ runId: run.id, summary: "done" });
    expect(finished.id).toBe(run.id);
  });
});

// ── Evidence idempotency & canonicalization (daedalus acceptance) ─────────────
//
// Verifies:
//   (a) same idempotencyKey + semantically identical data but different JSON key
//       order → treated as a replay → same evidence id returned.
//   (b) same idempotencyKey + real value change (different ref) →
//       WorkRpcError with domainCode WRKF_IDEMPOTENCY_MISMATCH.
//
describe("evidence idempotency & canonicalization via @wrkq/client", () => {
  let idemDir: string;
  let idemClient: WorkClient;
  let idemTaskId: string;

  beforeAll(async () => {
    idemDir = mkdtempSync(join(tmpdir(), "wrkq-client-idem-"));
    const idemDb = join(idemDir, "wrkq.db");
    const idemWfPath = join(idemDir, "workflow.json");
    writeFileSync(idemWfPath, JSON.stringify(WORKFLOW));
    const env = { ...process.env };
    delete env.ASP_PROJECT;
    delete env.WRKQ_PROJECT;
    execFileSync(WRKQADM, ["init", "--db", idemDb], { env, cwd: idemDir, stdio: "ignore" });
    idemClient = await createClient({
      command: WRKQ,
      dbPath: idemDb,
      actor: ACTOR,
      role: "coordinator",
      cwd: idemDir,
      env: { ...process.env, ASP_PROJECT: "", WRKQ_PROJECT: "" },
    });
    await idemClient.wrkf.workflow.install({ path: idemWfPath });
    const task = await idemClient.wrkq.task.create({
      title: "idem-canon-task",
      kind: "task",
      state: "open",
    });
    idemTaskId = task.id;
    await idemClient.wrkq.workflow.attach({
      task: idemTaskId,
      workflow: "itest_flow@1",
      actor: "agent:test",
    });
  });

  afterAll(async () => {
    try {
      if (idemClient) await idemClient.close();
    } finally {
      if (idemDir) rmSync(idemDir, { recursive: true, force: true });
    }
  });

  test("same idempotencyKey + different data key order → same evidence id (replay / canonicalization)", async () => {
    const idemKey = "canon:key-order:001";

    const e1 = await idemClient.wrkf.evidence.add({
      task: idemTaskId,
      kind: "implementation",
      ref: "git:canon-ref",
      summary: "canon test",
      facts: { verdict: "needs_patch" },
      data: { z: 3, a: 1, m: 2 },
      role: "coordinator",
      actor: "agent:test",
      idempotencyKey: idemKey,
    });
    expect(typeof e1.id).toBe("string");

    // Same key, same semantic data but different JSON key order — must replay.
    const e2 = await idemClient.wrkf.evidence.add({
      task: idemTaskId,
      kind: "implementation",
      ref: "git:canon-ref",
      summary: "canon test",
      facts: { verdict: "needs_patch" },
      data: { a: 1, m: 2, z: 3 }, // same values, different key order
      role: "coordinator",
      actor: "agent:test",
      idempotencyKey: idemKey,
    });

    // Server canonicalizes → same committed evidence id.
    expect(e2.id).toBe(e1.id);
  });

  test("same idempotencyKey + real value change → WRKF_IDEMPOTENCY_MISMATCH", async () => {
    const idemKey = "canon:mismatch:001";

    // Establish the committed record.
    await idemClient.wrkf.evidence.add({
      task: idemTaskId,
      kind: "implementation",
      ref: "git:original-ref",
      summary: "original",
      facts: { verdict: "needs_patch" },
      role: "coordinator",
      actor: "agent:test",
      idempotencyKey: idemKey,
    });

    // Same key, different ref (real value change) → mismatch.
    let err: unknown;
    try {
      await idemClient.wrkf.evidence.add({
        task: idemTaskId,
        kind: "implementation",
        ref: "git:different-ref", // changed — alters canonical hash
        summary: "original",
        facts: { verdict: "needs_patch" },
        role: "coordinator",
        actor: "agent:test",
        idempotencyKey: idemKey,
      });
    } catch (e) {
      err = e;
    }

    expect(err).toBeInstanceOf(WorkRpcError);
    expect(isWrkfError(err)).toBe(true);
    expect((err as WorkRpcError).domainCode).toBe("WRKF_IDEMPOTENCY_MISMATCH");
  });
});

// ── Full agent-loop flow via `wrkf rpc --stdio` entrypoint ───────────────────
//
// Mirrors the wrkq lifecycle test above but spawns command:"wrkf". Asserts
// equivalent observable behavior: same task ids, same transition outcome, same
// effect kinds, same error codes on stale CAS and wrong lease token.
//
describe("WorkClient over real `wrkf rpc --stdio`", () => {
  let wrkfDir: string;
  let wrkfDb: string;
  let wrkfWfPath: string;
  let wrkfClient: WorkClient;

  beforeAll(() => {
    wrkfDir = mkdtempSync(join(tmpdir(), "wrkq-client-wrkf-e2e-"));
    wrkfDb = join(wrkfDir, "wrkq.db");
    wrkfWfPath = join(wrkfDir, "workflow.json");
    writeFileSync(wrkfWfPath, JSON.stringify(WORKFLOW));
    const env = { ...process.env };
    delete env.ASP_PROJECT;
    delete env.WRKQ_PROJECT;
    execFileSync(WRKQADM, ["init", "--db", wrkfDb], { env, cwd: wrkfDir, stdio: "ignore" });
  });

  afterAll(async () => {
    try {
      if (wrkfClient) await wrkfClient.close();
    } finally {
      if (wrkfDir) rmSync(wrkfDir, { recursive: true, force: true });
    }
  });

  test("agent-loop-ready lifecycle via wrkf entrypoint — equivalent observable behavior", async () => {
    wrkfClient = await createClient({
      command: WRKF,
      dbPath: wrkfDb,
      actor: ACTOR,
      role: "coordinator",
      cwd: wrkfDir,
      env: { ...process.env, ASP_PROJECT: "", WRKQ_PROJECT: "" },
    });

    // ── wrkf.workflow.install ──
    const installed = await wrkfClient.wrkf.workflow.install({ path: wrkfWfPath });
    expect(installed.id).toBe("itest_flow");
    expect(installed.installed).toBe(true);

    // ── wrkq.task.create ──
    const task = await wrkfClient.wrkq.task.create({
      title: "wrkf-entrypoint task",
      kind: "task",
      state: "open",
      idempotencyKey: "itest:wrkf-e2e:create",
    });
    expect(task.id).toBe("T-00001");
    expect(task.state).toBe("open");

    // Idempotent replay → same task.
    const again = await wrkfClient.wrkq.task.create({
      title: "wrkf-entrypoint task",
      kind: "task",
      state: "open",
      idempotencyKey: "itest:wrkf-e2e:create",
    });
    expect(again.id).toBe(task.id);

    // ── wrkq.workflow.attach ──
    const attached = await wrkfClient.wrkq.workflow.attach({
      task: task.id,
      workflow: "itest_flow@1",
      actor: "agent:agent-loop",
      idempotencyKey: `itest:wrkf-e2e:attach:${task.id}`,
    });
    expect(attached.attached).toBe(true);
    expect(attached.instance.revision).toBe(0);
    expect(attached.instance.phase).toBe("plan");

    // ── wrkf.run.start ──
    const run = await wrkfClient.wrkf.run.start({
      task: task.id,
      role: "coordinator",
      actor: "agent:agent-loop",
      externalRunRef: "wrkf-loop-run-1",
      idempotencyKey: `itest:wrkf-e2e:run:${task.id}:1`,
    });
    expect(typeof run.id).toBe("string");

    // ── wrkf.evidence.add (needs_patch, then ready with runId) ──
    const needsPatch = await wrkfClient.wrkf.evidence.add({
      task: task.id,
      kind: "implementation",
      ref: "git:abc",
      summary: "needs patch",
      facts: { verdict: "needs_patch" },
      role: "coordinator",
      actor: "agent:agent-loop",
    });
    expect(needsPatch.facts?.verdict).toBe("needs_patch");

    const staleHash = (await wrkfClient.wrkq.workflow.inspect({ task: task.id })).instance.contextHash;

    const ready = await wrkfClient.wrkf.evidence.add({
      task: task.id,
      kind: "implementation",
      ref: "git:def",
      summary: "ready",
      facts: { verdict: "ready" },
      role: "coordinator",
      actor: "agent:agent-loop",
      runId: run.id,
    });
    expect(ready.facts?.verdict).toBe("ready");
    expect(ready.runId).toBe(run.id);

    // ── CAS stale error path: stale contextHash → typed WorkRpcError ──
    let casErr: unknown;
    try {
      await wrkfClient.wrkf.transition.apply({
        task: task.id,
        transition: "plan_ready",
        role: "coordinator",
        actor: "agent:agent-loop",
        expectRevision: 0,
        contextHash: staleHash,
      });
    } catch (e) {
      casErr = e;
    }
    expect(casErr).toBeInstanceOf(WorkRpcError);
    expect(isWrkfError(casErr)).toBe(true);
    expect((casErr as WorkRpcError).retryable).toBe(true);

    // ── wrkf.transition.apply (commit) with fresh CAS ──
    const fresh = await wrkfClient.wrkq.workflow.inspect({ task: task.id });
    const transitioned = await wrkfClient.wrkf.transition.apply({
      task: task.id,
      transition: "plan_ready",
      role: "coordinator",
      actor: "agent:agent-loop",
      expectRevision: fresh.instance.revision,
      contextHash: fresh.instance.contextHash,
      idempotencyKey: `itest:wrkf-e2e:transition:${task.id}:plan_ready:${fresh.instance.revision}`,
    });
    expect(transitioned.instanceId).toBe(attached.instance.id);
    expect(transitioned.revision).toBe(1);
    expect(transitioned.effects.length).toBe(1);
    expect(transitioned.effects[0]!.kind).toBe("wake_role");

    // ── wrkf.effect.list / claim / ack ──
    const effects = await wrkfClient.wrkf.effect.list({ task: task.id, all: true });
    expect(effects.length).toBe(1);

    const claim = await wrkfClient.wrkf.effect.claim({ adapter: "wake_role", limit: 5, leaseMs: 60000 });
    expect(claim.effects.length).toBe(1);
    expect(typeof claim.leaseToken).toBe("string");
    const effId = claim.effects[0]!.id;

    const acked = await wrkfClient.wrkf.effect.ack({ effectId: effId, leaseToken: claim.leaseToken });
    expect(acked.id).toBe(effId);

    // ── Wrong lease token → WRKF_LEASE_CONFLICT ──
    let leaseErr: unknown;
    try {
      await wrkfClient.wrkf.effect.fail({ effectId: effId, leaseToken: "bogus_lease", reason: "nope" });
    } catch (e) {
      leaseErr = e;
    }
    expect(isWrkfError(leaseErr)).toBe(true);

    // ── wrkf.run.finish ──
    const finished = await wrkfClient.wrkf.run.finish({ runId: run.id, summary: "done" });
    expect(finished.id).toBe(run.id);
  });
});
