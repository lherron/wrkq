/**
 * namespace-contract.test.ts — enforces the public surface shape (spec §7.2,
 * §13).
 *
 * Runtime: the client exposes exactly `rpc`, `wrkq`, `wrkf` business access; no
 * root business namespaces exist.
 *
 * Type-level: accessing a root business namespace is a COMPILE error. The
 * `@ts-expect-error` directives below are validated by `bun x tsc --noEmit` —
 * if any access stopped being an error, tsc would flag the unused directive.
 */

import { describe, expect, test } from "bun:test";
import { createClient, type WorkClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";

describe("public surface (runtime)", () => {
  test("client exposes rpc, wrkq, wrkf and no root business namespaces", async () => {
    const client = await createClient({ transport: new FakeTransport(), autoInitialize: false });

    expect(typeof client.rpc.initialize).toBe("function");
    expect(typeof client.rpc.shutdown).toBe("function");
    expect(typeof client.call).toBe("function");
    expect(typeof client.close).toBe("function");
    expect(typeof client.kill).toBe("function");

    // Business access is namespaced.
    expect(typeof client.wrkq.task.create).toBe("function");
    expect(typeof client.wrkq.container.taskCounts).toBe("function");
    expect(typeof client.wrkq.workflow.attach).toBe("function");
    expect(typeof client.wrkf.event.query).toBe("function");
    expect(typeof client.wrkf.transition.apply).toBe("function");
    expect(typeof client.wrkf.suspension.resolve).toBe("function");
    const wrkfWithInstance = client.wrkf as unknown as { instance?: { cancel?: unknown } };
    expect(typeof wrkfWithInstance.instance?.cancel).toBe("function");
    expect(typeof client.wrkf.role.set).toBe("function");
    expect(typeof client.wrkf.effect.claim).toBe("function");

    // No root business namespaces.
    const anyClient = client as unknown as Record<string, unknown>;
    for (const forbidden of [
      "workflow",
      "task",
      "evidence",
      "event",
      "obligation",
      "check",
      "hook",
      "transition",
      "run",
      "effect",
    ]) {
      expect(anyClient[forbidden]).toBeUndefined();
    }
  });

  test("T-05381 removes the legacy actor admin facade from the public client", async () => {
    const client = await createClient({ transport: new FakeTransport(), autoInitialize: false });

    const wrkq = client.wrkq as unknown as Record<string, unknown>;
    const admin = wrkq.admin as Record<string, unknown> | undefined;
    expect(admin).toBeDefined();

    // T-05381 removes wrkq.admin.legacyActor.* as live actor scaffolding.
    expect(admin?.legacyActor).toBeUndefined();
  });
});

// ── Type-level negatives (validated by tsc --noEmit, not at runtime) ──────────
// eslint-disable-next-line @typescript-eslint/no-unused-vars
function _typeNegatives(c: WorkClient) {
  // @ts-expect-error root client.workflow is forbidden (spec §7.2)
  c.workflow;
  // @ts-expect-error root client.task is forbidden
  c.task;
  // @ts-expect-error root client.evidence is forbidden
  c.evidence;
  // @ts-expect-error root client.event is forbidden
  c.event;
  // @ts-expect-error root client.transition is forbidden
  c.transition;
  // @ts-expect-error root client.run is forbidden
  c.run;
  // @ts-expect-error root client.effect is forbidden
  c.effect;
  // @ts-expect-error no wrkf.task namespace (task mutation is wrkq-only)
  c.wrkf.task;
  c.wrkf.role.set({ task: "T-00001", roleMap: { implementer: "agent:cody" } });
  // @ts-expect-error workflow attach is a wrkq verb, not wrkf
  c.wrkf.workflow.attach;
}

describe("type negatives compile-guard", () => {
  test("the negative-type fixture exists (enforced by tsc)", () => {
    expect(typeof _typeNegatives).toBe("function");
  });
});
