/**
 * principal-ref.test.ts — T-05372 wrkf actor→principal_ref cutover.
 *
 * Proves the client forwards canonical `principal_ref` (snake_case) on live
 * wrkf participant-identity calls and never silently rewrites it to legacy
 * `actor`. Params are forwarded VERBATIM, so the TS field name equals the wire
 * field name. Uses FakeTransport; no subprocess.
 *
 * Contract: docs/wrkf-rpc.md, docs/wrkq-wrkf-rpc.md §6.3, §7.
 */

import { describe, expect, test } from "bun:test";
import { createClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";
import type { WrkfRoleBinding } from "../src/wrkf/types";

const BINDING: WrkfRoleBinding = {
  instanceId: "wfi_1",
  role: "implementer",
  principal_ref: "agent:cody",
  deliveryRef: "cody@wrkq:T-1",
  lane: "main",
  bindingMode: "required",
  boundAt: "2026-06-30T00:00:00Z",
};

describe("wrkf principal_ref cutover (T-05372)", () => {
  test("role.bind forwards principal_ref verbatim and not legacy actor", async () => {
    const transport = new FakeTransport().onResult("wrkf.role.bind", BINDING);
    const client = await createClient({ transport, autoInitialize: false });

    const bound = await client.wrkf.role.bind({
      task: "T-00001",
      role: "implementer",
      principal_ref: "agent:cody",
      deliveryRef: "cody@wrkq:T-1",
      lane: "main",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkf.role.bind");
    // Pass-through: the snake_case identity reaches the wire untouched.
    expect(frame.params).toMatchObject({
      role: "implementer",
      principal_ref: "agent:cody",
    });
    // The client must NOT translate principal_ref into a legacy actor field.
    expect(frame.params).not.toHaveProperty("actor");
    expect(bound.principal_ref).toBe("agent:cody");
    expect("actor" in (bound as unknown as Record<string, unknown>)).toBe(false);
  });

  test("transition.apply forwards principal_ref identity verbatim", async () => {
    const transport = new FakeTransport().onResult("wrkf.transition.apply", {
      task: "T-00001",
      instanceId: "wfi_1",
      state: { status: "active", phase: "red" },
      revision: 1,
      contextHash: "sha256:abc",
      eventId: "wfe_1",
      effects: [],
      obligations: [],
    });
    const client = await createClient({ transport, autoInitialize: false });

    await client.wrkf.transition.apply({
      task: "T-00001",
      transition: "author_red",
      role: "tester",
      principal_ref: "agent:tester",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkf.transition.apply");
    expect(frame.params).toMatchObject({
      transition: "author_red",
      role: "tester",
      principal_ref: "agent:tester",
    });
    expect(frame.params).not.toHaveProperty("actor");
  });
});
