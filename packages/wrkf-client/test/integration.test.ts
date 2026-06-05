/**
 * integration.test.ts — substrate acceptance (replaces ACP for this pass).
 *
 * Spawns the REAL `wrkf rpc --stdio` binary against a temp, freshly-migrated
 * wrkq DB and drives the lifecycle end-to-end through WrkfClient:
 *
 *   initialize → workflow.install → task.attach → task.inspect → next
 *   → evidence.add (needs_patch, then ready)
 *   → transition.apply with a CAS stale-contextHash precondition (error path)
 *   → transition.apply (commit) → obligation.list → effect.claim/ack
 *   → run.start → run.bindExternal → run.finish
 *
 * Asserts typed results + a CAS stale error path (typed WrkfRpcError).
 *
 * Contract: docs/wrkf-rpc.md §2, §4, §5, §5.1, §7.
 *
 * Binary discovery: env WRKF_BIN / WRKQ_BIN / WRKQADM_BIN, else PATH.
 * Requires `just install` (wrkf, wrkq, wrkqadm on ~/.local/bin) to have run.
 */

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync, chmodSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { WrkfClient } from "../src/client";
import { WrkfRpcError } from "../src/errors";
import type { TransitionResult } from "../src/types";

// ASP_PROJECT=wrkq in the agent env makes wrkq path-resolution look in a
// non-existent "wrkq" container. The smoke harness unsets it; we do too.
delete process.env.ASP_PROJECT;
delete process.env.WRKQ_PROJECT;

const WRKF = process.env.WRKF_BIN ?? "wrkf";
const WRKQ = process.env.WRKQ_BIN ?? "wrkq";
const WRKQADM = process.env.WRKQADM_BIN ?? "wrkqadm";
const ACTOR = "human:local-human";

const WORKFLOW = {
  schemaVersion: "wrkf.workflow-template.v0",
  id: "itest_flow",
  version: "1",
  kind: "agent_first_workflow",
  initial: { status: "active", phase: "plan" },
  roles: {
    coordinator: { description: "Coordinates the integration workflow" },
    supervisor: { description: "Handles recovery" },
  },
  states: [
    { status: "active", phase: "plan" },
    { status: "active", phase: "done" },
    { status: "active", phase: "error" },
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
  obligationKinds: { cleanup: { description: "Cleanup duty" } },
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
          obligations: [
            {
              kind: "cleanup",
              ownerRole: "coordinator",
              blocking: true,
              reason: "cleanup before close",
            },
          ],
          effects: [{ kind: "wake_role", role: "coordinator", reason: "ready to finish" }],
        },
      ],
    },
  ],
};

const HOOKS = { schemaVersion: "wrkf.hook-catalog.v0", hooks: {} };

let dir: string;
let dbPath: string;
let workflowPath: string;
let hooksPath: string;
let client: WrkfClient;

beforeAll(() => {
  dir = mkdtempSync(join(tmpdir(), "wrkf-client-itest-"));
  dbPath = join(dir, "wrkq.db");
  workflowPath = join(dir, "workflow.json");
  hooksPath = join(dir, "hooks.json");

  writeFileSync(workflowPath, JSON.stringify(WORKFLOW));
  writeFileSync(hooksPath, JSON.stringify(HOOKS));

  const env = { ...process.env };
  delete env.ASP_PROJECT;
  delete env.WRKQ_PROJECT;

  // wrkq derives its project from cwd; run from the temp dir to get the default
  // project (the repo cwd would resolve a non-existent "wrkq" container).
  execFileSync(WRKQADM, ["init", "--db", dbPath], { env, cwd: dir, stdio: "ignore" });
  execFileSync(
    WRKQ,
    ["--db", dbPath, "--as", "local-human", "touch", "inbox/itest", "-t", "wrkf-client itest"],
    { env, cwd: dir, stdio: "ignore" },
  );

  client = WrkfClient.spawn({
    command: WRKF,
    dbPath,
    actor: ACTOR,
    role: "coordinator",
    hookCatalogPath: hooksPath,
    env,
    cwd: dir,
  });
});

afterAll(async () => {
  try {
    if (client) await client.close();
  } finally {
    if (dir) rmSync(dir, { recursive: true, force: true });
  }
});

describe("WrkfClient over real `wrkf rpc --stdio`", () => {
  test("full lifecycle with CAS, evidence, runs, effects", async () => {
    // ── initialize (§2) ──
    const init = await client.initialize({ client: { name: "itest", version: "0" } });
    expect(init.protocolVersion).toBe("2026-06-01");
    expect(init.database.path).toBe(dbPath);
    expect(init.capabilities.effectClaimLease).toBe(true);
    expect(init.schemaHash.startsWith("sha256:")).toBe(true);

    // ── workflow.install (§5: InstallResult) ──
    const installed = await client.workflow.install({ path: workflowPath });
    expect(installed.id).toBe("itest_flow");
    expect(installed.version).toBe("1");
    expect(installed.installed).toBe(true);

    // ── task.attach ──
    const attached = await client.task.attach({ task: "T-00001", workflow: "itest_flow@1" });
    expect(attached.phase).toBe("plan");
    expect(attached.revision).toBe(0);

    // ── task.inspect ──
    const inspected = await client.task.inspect({ task: "T-00001" });
    expect(inspected.templateId).toBe("itest_flow");

    // ── next: must want evidence ──
    const next = await client.next({ task: "T-00001" });
    expect(next.actions.some((a) => a.kind === "collect_evidence")).toBe(true);

    // ── evidence.add: needs_patch first, then snapshot a (soon-to-be) stale ctx hash ──
    const needsPatch = await client.evidence.add({
      task: "T-00001",
      kind: "implementation",
      ref: "git:abc123",
      summary: "needs patch",
      facts: { verdict: "needs_patch" },
    });
    expect(needsPatch.facts?.verdict).toBe("needs_patch");

    const staleContextHash = (await client.task.inspect({ task: "T-00001" })).contextHash as string;
    expect(typeof staleContextHash).toBe("string");

    // ── evidence.add: ready — this mutates the instance context, staling the snapshot ──
    const ready = await client.evidence.add({
      task: "T-00001",
      kind: "implementation",
      ref: "git:def456",
      summary: "ready",
      facts: { verdict: "ready" },
    });
    expect(ready.facts?.verdict).toBe("ready");

    const evidence = await client.evidence.list({ task: "T-00001" });
    expect(evidence.length).toBe(2);

    // ── CAS stale error path: apply with the stale contextHash → typed WrkfRpcError ──
    let casErr: unknown;
    try {
      await client.transition.apply({
        task: "T-00001",
        transition: "plan_ready",
        role: "coordinator",
        expectRevision: 0,
        contextHash: staleContextHash,
        idempotencyKey: "itest-stale",
      });
    } catch (e) {
      casErr = e;
    }
    expect(casErr).toBeInstanceOf(WrkfRpcError);
    const rpcErr = casErr as WrkfRpcError;
    expect(rpcErr.isDomainError).toBe(true);
    expect(rpcErr.code).toBe("WRKF_CONTEXT_MISMATCH");
    expect(rpcErr.retryable).toBe(true);

    // ── transition.apply: commit (no stale precondition) → typed TransitionResult ──
    const transition: TransitionResult = await client.transition.apply({
      task: "T-00001",
      transition: "plan_ready",
      role: "coordinator",
      expectRevision: 0,
      idempotencyKey: "itest-plan-ready",
    });
    expect(transition.task).toBe("T-00001");
    expect(transition.revision).toBe(1);
    expect(typeof transition.contextHash).toBe("string");
    expect(typeof transition.eventId).toBe("string");
    expect(Array.isArray(transition.effects)).toBe(true);
    expect(Array.isArray(transition.obligations)).toBe(true);
    // state is the structured {status, phase, outcome} object on the wire
    const state = transition.state as { phase?: string };
    expect(state.phase).toBe("done");

    // ── idempotency replay: same key + same request → original committed result (§5.1) ──
    const replay: TransitionResult = await client.transition.apply({
      task: "T-00001",
      transition: "plan_ready",
      role: "coordinator",
      expectRevision: 0,
      idempotencyKey: "itest-plan-ready",
    });
    expect(replay.revision).toBe(1);
    expect(replay.eventId).toBe(transition.eventId);

    // ── obligation.list: the transition created a blocking cleanup obligation ──
    const obligations = await client.obligation.list({ task: "T-00001" });
    expect(obligations.length).toBeGreaterThanOrEqual(1);
    expect(obligations[0]!.status).toBe("open");

    // ── effect.claim / ack: lease then deliver ──
    const effects = await client.effect.list({ task: "T-00001", all: true });
    expect(effects.length).toBeGreaterThanOrEqual(1);
    const effId = effects[0]!.id;

    const claim = await client.effect.claim({
      adapter: "itest",
      limit: 1,
      leaseMs: 30000,
      task: "T-00001",
    });
    expect(claim.effects[0]!.id).toBe(effId);
    expect(typeof claim.leaseToken).toBe("string");
    expect(claim.leaseToken.length).toBeGreaterThan(0);

    const acked = await client.effect.ack({ effectId: effId, leaseToken: claim.leaseToken });
    expect(acked.status).toBe("delivered");

    // ── effect.ack with a wrong lease token → WRKF_LEASE_CONFLICT (typed) ──
    // (effect already delivered; a bogus token must not succeed)
    let leaseErr: unknown;
    try {
      await client.effect.ack({ effectId: effId, leaseToken: "bogus-token" });
    } catch (e) {
      leaseErr = e;
    }
    expect(leaseErr).toBeInstanceOf(WrkfRpcError);

    // ── run.start → bindExternal → finish (execution-attempt shape, §5.1) ──
    const run = await client.run.start({
      task: "T-00001",
      role: "coordinator",
      actor: ACTOR,
      idempotencyKey: "itest-run-1",
    });
    expect(run.status).toBe("active");

    const bound = await client.run.bindExternal({
      runId: run.id,
      externalRunRef: "itest/external/1",
      idempotencyKey: "itest-bind-1",
    });
    expect(bound.externalRunRef).toBe("itest/external/1");

    const finished = await client.run.finish({ runId: run.id, summary: "done" });
    expect(finished.status).toBe("completed");

    const runs = await client.run.list({ task: "T-00001" });
    expect(runs.length).toBe(1);
  });
});
