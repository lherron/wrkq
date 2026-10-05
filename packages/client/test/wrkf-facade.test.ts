/**
 * wrkf-facade.test.ts — wrkf namespace facade.
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { isWrkfError } from "../src/errors";
import { FakeTransport } from "../src/testing/fake-transport";
import type { WrkfEventQueryResult, WrkfTransitionResult } from "../src/wrkf/types";
import { clientWith, rejectionOf } from "./support/client-fixtures";

const MOCK_TRANSITION_RESULT: WrkfTransitionResult = {
  task: "T-00001",
  instanceId: "wfi_abc123",
  state: { status: "active", phase: "done" },
  revision: 1,
  eventId: "wfe_000002",
  effects: [],
  obligations: [],
};

const MOCK_EVENT_QUERY_RESULT: WrkfEventQueryResult = {
  items: [
    {
      id: "wfe_000002",
      eventType: "workflow.transitioned",
      instanceId: "wfi_abc123",
      seq: 2,
      task: {
        uuid: "task-u-1",
        id: "T-00001",
        slug: "my-task",
        projectUuid: "p-1",
        projectId: "P-00001",
        projectSlug: "wrkq",
        riskClass: "medium",
      },
      transition: "author_red",
      outcome: "red_recorded",
      fromPhase: "intake",
      toPhase: "red",
      occurredAt: "2026-06-15T14:00:00Z",
      beforeRevision: 0,
      afterRevision: 1,
      principal_ref: "agent:tester",
      role: "tester",
      matchingRoleBindings: [
        {
          instanceId: "wfi_abc123",
          role: "tester",
          principal_ref: "agent:tester",
          bindingMode: "required",
          boundAt: "2026-06-15T13:59:00Z",
        },
      ],
    },
  ],
  nextCursor: "cursor-1",
  hasMore: true,
};

describe("wrkf namespace", () => {
  test("workflow template lifecycle methods preserve typed current-row results", async () => {
    const row = {
      template: { id: "f", version: "1" },
      hash: "sha256:abc",
      discontinuedAt: "2026-07-14T12:00:00Z",
      discontinuedBy: "agent:curator",
    };
    const transport = new FakeTransport()
      .onResult("wrkf.workflow.discontinue", row)
      .onResult("wrkf.workflow.reinstate", { template: row.template, hash: row.hash });
    const client = await clientWith(transport);

    const discontinued = await client.wrkf.workflow.discontinue({
      ref: "f@1",
      principal_ref: "agent:curator",
    });
    const reinstated = await client.wrkf.workflow.reinstate({ ref: "f@1" });

    expect(transport.capturedRequests.map((request) => request.method)).toEqual([
      "wrkf.workflow.discontinue",
      "wrkf.workflow.reinstate",
    ]);
    expect(transport.capturedRequests[0]!.params).toEqual({
      ref: "f@1",
      principal_ref: "agent:curator",
    });
    expect(discontinued.discontinuedBy).toBe("agent:curator");
    expect(reinstated.discontinuedAt).toBeUndefined();
  });

  test("workflow template facades send content only and preflight body limits", async () => {
    const transport = new FakeTransport()
      .onResult("wrkf.workflow.validate", { valid: true })
      .onResult("wrkf.workflow.diff", { old: {}, new: {}, sameHash: true })
      .onResult("wrkf.workflow.install", { id: "f", version: "1", hash: "sha256:x", installed: true });
    const client = await clientWith(transport);

    await client.wrkf.workflow.validate({ body: "{}", sourceName: "caller/workflow.json" });
    await client.wrkf.workflow.diff({ oldBody: "{}", newBody: "{}" });
    await client.wrkf.workflow.install({ body: "{}", sourceName: "caller/workflow.json" });

    expect(transport.capturedRequests.map((request) => request.method)).toEqual([
      "wrkf.workflow.validate",
      "wrkf.workflow.diff",
      "wrkf.workflow.install",
    ]);
    expect(transport.capturedRequests[2]!.params).toEqual({
      body: "{}",
      sourceName: "caller/workflow.json",
    });
    expect(() => client.wrkf.workflow.install({ body: "x".repeat((1 << 20) + 1) })).toThrow(
      "1048576-byte template body limit",
    );
    expect(transport.capturedRequests).toHaveLength(3);
  });

  test("action facade preserves opaque sourceIdentity bindings", async () => {
    const sourceIdentity = "change-v1:implement-v4-1";
    const transport = new FakeTransport()
      .onResult("wrkf.action.next", {
        candidates: [
          {
            instanceId: "wfi_1",
            task: "T-00001",
            semanticActionKey: "verify:wfi_1:r1",
            action: "verify",
            transition: "verify_complete",
            role: "verifier",
            requiredEvidenceKind: "verify_result",
            expectedStateRevision: 1,
            expectedState: { status: "active", phase: "implemented" },
            rank: 1,
            source: {
              sourceRunId: "run-implement-1",
              sourceEvidenceId: "ev-implement-1",
              sourceIdentity,
            },
          },
        ],
      })
      .onResult("wrkf.action.claim", {
        binding: {
          run: {
            id: "run-verify-1",
            instanceId: "wfi_1",
            semanticActionKey: "verify:wfi_1:r1",
            action: "verify",
            role: "verifier",
            attempt: 1,
            status: "running",
            source: { sourceRunId: "run-implement-1", sourceIdentity },
            startedAt: "2026-07-10T00:00:00Z",
          },
        },
      });
    const client = await clientWith(transport);

    const next = await client.wrkf.action.next({ task: "T-00001" });
    const claim = await client.wrkf.action.claim({
      task: "T-00001",
      runnerId: "runner-1",
      agentRef: "agent:larry",
      leaseMs: 60_000,
      priorRun: null,
    });

    expect(next.candidates[0]?.source?.sourceIdentity).toBe(sourceIdentity);
    expect(claim.binding?.run.source?.sourceIdentity).toBe(sourceIdentity);
    expect(transport.capturedRequests[1]?.params).toMatchObject({ priorRun: null });
    expect(transport.capturedRequests.map((request) => request.method)).toEqual([
      "wrkf.action.next",
      "wrkf.action.claim",
    ]);
  });

  test("transition.apply sends frame, forwards CAS params, returns typed result", async () => {
    const transport = new FakeTransport().onResult(
      "wrkf.transition.apply",
      MOCK_TRANSITION_RESULT,
    );
    const client = await clientWith(transport);

    const result = await client.wrkf.transition.apply({
      task: "T-00001",
      transition: "plan_ready",
      role: "coordinator",
      principal_ref: "human:local",
      expectRevision: 0,
      idempotencyKey: "k:plan_ready:0",
      dryRun: false,
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkf.transition.apply");
    expect(frame.params).toMatchObject({
      task: "T-00001",
      transition: "plan_ready",
      expectRevision: 0,
    });
    expect(result.instanceId).toBe("wfi_abc123");
    expect(result.revision).toBe(1);
    expect(Array.isArray(result.effects)).toBe(true);
  });

  test("suspension.resolve sends frame, forwards id gate + CAS, returns typed result", async () => {
    const transport = new FakeTransport().onResult("wrkf.suspension.resolve", {
      task: "T-00001",
      instanceId: "wfi_abc123",
      suspensionId: "sus_000001",
      disposition: "resume",
      state: { status: "active", phase: "ready" },
      revision: 2,
      eventId: "wfe_000003",
      effects: [{ id: "eff_1", status: "pending", kind: "resume_notice" }],
    });
    const client = await clientWith(transport);

    const result = await client.wrkf.suspension.resolve({
      suspensionId: "sus_000001",
      disposition: "resume",
      explanation: "operator cleared the park",
      expectRevision: 1,
      role: "coordinator",
      principal_ref: "human:local",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkf.suspension.resolve");
    expect(frame.params).toMatchObject({
      suspensionId: "sus_000001",
      disposition: "resume",
      explanation: "operator cleared the park",
      expectRevision: 1,
    });
    expect(result.suspensionId).toBe("sus_000001");
    expect(result.disposition).toBe("resume");
    expect(result.revision).toBe(2);
    expect(result.eventId).toBe("wfe_000003");
    expect(result.effects[0]!.id).toBe("eff_1");
  });

  test("suspension.resolve surfaces WRKF_SUSPENSION_NOT_FOUND as a typed wrkf error", async () => {
    const transport = new FakeTransport().onError("wrkf.suspension.resolve", {
      code: -32025,
      message: "no active suspension matches id sus_999",
      data: { code: "WRKF_SUSPENSION_NOT_FOUND", retryable: false },
    });
    const client = await clientWith(transport);

    const err = await rejectionOf(client.wrkf.suspension.resolve({ suspensionId: "sus_999", disposition: "close" }));
    expect(isWrkfError(err)).toBe(true);
    expect(err.domainCode).toBe("WRKF_SUSPENSION_NOT_FOUND");
    expect(err.method).toBe("wrkf.suspension.resolve");
  });

  test("event.query sends replay filters and returns a typed page", async () => {
    const transport = new FakeTransport().onResult("wrkf.event.query", MOCK_EVENT_QUERY_RESULT);
    const client = await clientWith(transport);

    const result = await client.wrkf.event.query({
      eventType: "workflow.transitioned",
      fromPhase: "intake",
      toPhase: "red",
      excludeRiskClass: "low",
      boundRole: "tester",
      limit: 100,
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkf.event.query");
    expect(frame.params).toMatchObject({
      fromPhase: "intake",
      toPhase: "red",
      excludeRiskClass: "low",
      boundRole: "tester",
    });
    expect(result.items[0]!.id).toBe("wfe_000002");
    expect(result.items[0]!.matchingRoleBindings[0]!.role).toBe("tester");
    expect(result.nextCursor).toBe("cursor-1");
    expect(result.hasMore).toBe(true);
  });

  test("evidence.add forwards runId and provenance (spec §9.7)", async () => {
    const transport = new FakeTransport().onResult("wrkf.evidence.add", {
      id: "ev_1",
      kind: "implementation",
      runId: "run_000001",
      contentHash: "sha256:abc",
      build: { id: "b1", version: "2026.06.15", env: "ci" },
    });
    const client = await clientWith(transport);
    const ev = await client.wrkf.evidence.add({
      task: "T-00001",
      kind: "implementation",
      facts: { verdict: "ready" },
      runId: "run_000001",
      contentHash: "sha256:abc",
      build: { id: "b1", version: "2026.06.15", env: "ci" },
    });
    expect(transport.capturedRequests[0]!.params).toMatchObject({
      runId: "run_000001",
      contentHash: "sha256:abc",
      build: { id: "b1", version: "2026.06.15", env: "ci" },
    });
    expect(ev.runId).toBe("run_000001");
    expect(ev.contentHash).toBe("sha256:abc");
  });

  test("ledger facade exposes append + list without mutation verbs", async () => {
    const entry = {
      seq: 1,
      uuid: "deadc0de-0000-4000-8000-000000000001",
      instanceId: "wfi_live_instance",
      taskId: "T-00001",
      ts: "2026-07-11T12:00:00Z",
      kind: "runner_start",
      aboutPrincipalRef: "agent:cody",
      writtenBy: "agent:smokey",
      body: { refs: { logs: { narration: "/tmp/narration.log" } } },
    };
    const transport = new FakeTransport()
      .onResult("wrkf.ledger.append", entry)
      .onResult("wrkf.ledger.list", { entries: [entry] });
    const client = await clientWith(transport);

    const appended = await client.wrkf.ledger.append({
      taskId: "T-00001",
      kind: "runner_start",
      aboutPrincipalRef: "agent:cody",
      body: { refs: { logs: { narration: "/tmp/narration.log" } } },
    });
    const listed = await client.wrkf.ledger.list({ taskId: "T-00001" });

    expect(appended.instanceId).toBe("wfi_live_instance");
    expect(listed.entries[0]!.writtenBy).toBe("agent:smokey");
    expect(transport.capturedRequests.map((r) => r.method)).toEqual([
      "wrkf.ledger.append",
      "wrkf.ledger.list",
    ]);
    expect(transport.capturedRequests[0]!.params).not.toHaveProperty("writtenBy");
  });

  test("role facade forwards list, bind, unbind, and set", async () => {
    const binding = {
      instanceId: "wfi_1",
      role: "implementer",
      principal_ref: "agent:cody",
      deliveryRef: "cody@wrkq:T-1",
      lane: "main",
      bindingMode: "required",
      boundAt: "2026-06-15T00:00:00Z",
    };
    const transport = new FakeTransport()
      .onResult("wrkf.role.bind", binding)
      .onResult("wrkf.role.list", [binding])
      .onResult("wrkf.role.unbind", [])
      .onResult("wrkf.role.set", [binding]);
    const client = await clientWith(transport);

    await client.wrkf.role.bind({
      task: "T-00001",
      role: "implementer",
      principal_ref: "agent:cody",
      deliveryRef: "cody@wrkq:T-1",
      lane: "main",
    });
    await client.wrkf.role.list({ task: "T-00001" });
    await client.wrkf.role.unbind({ task: "T-00001", role: "implementer", principal_ref: "agent:cody" });
    await client.wrkf.role.set({ task: "T-00001", roleMap: { implementer: "agent:cody" } });

    expect(transport.capturedRequests.map((r) => r.method)).toEqual([
      "wrkf.role.bind",
      "wrkf.role.list",
      "wrkf.role.unbind",
      "wrkf.role.set",
    ]);
  });

  test("effect.claim returns lease token + expiry", async () => {
    const transport = new FakeTransport().onResult("wrkf.effect.claim", {
      effects: [{ id: "eff_1", status: "leased" }],
      leaseToken: "lease_abc",
      leaseExpiresAt: "2026-06-30T01:00:00Z",
    });
    const client = await clientWith(transport);
    const claim = await client.wrkf.effect.claim({ adapter: "wake_role", limit: 5, leaseMs: 60000 });
    expect(claim.leaseToken).toBe("lease_abc");
    expect(claim.effects[0]!.id).toBe("eff_1");
  });

  test("gap-method facades forward evidence, obligation, supervisor, and bounded watch calls", async () => {
    const transport = new FakeTransport()
      .onResult("wrkf.evidence.schema", { kind: "red_test", class: "test" })
      .onResult("wrkf.obligation.create", { id: "obl_1", kind: "review", blocking: true })
      .onResult("wrkf.supervisor.call", { id: "eff_1", kind: "supervisor_call", status: "pending" })
      .onResult("wrkf.supervisor.escalate", { id: "eff_2", kind: "supervisor_escalation", status: "pending" })
      .onResult("wrkf.watch.snapshot", {
        target: { kind: "task", selector: "T-00001", instanceId: "wfi_1" },
        until: "terminal",
        met: false,
        class: "pending",
        exitCode: 0,
        status: "active",
      })
      .onResult("wrkf.watch.events", {
        events: [{ id: "wfe_1", seq: 1, type: "workflow.attached" }],
        nextCursor: "opaque-cursor",
      });
    const client = await clientWith(transport);

    const schema = await client.wrkf.evidence.schema({ task: "T-00001", kind: "red_test" });
    const obligation = await client.wrkf.obligation.create({ task: "T-00001", kind: "review", blocking: true });
    await client.wrkf.supervisor.call({ task: "T-00001", reason: "attention" });
    await client.wrkf.supervisor.escalate({ task: "T-00001", reason: "urgent" });
    const snapshot = await client.wrkf.watch.snapshot({ selector: "T-00001", until: "terminal" });
    const events = await client.wrkf.watch.events({ selector: "T-00001", afterCursor: "cursor-0", limit: 100 });

    expect(schema.kind).toBe("red_test");
    expect(obligation.id).toBe("obl_1");
    expect(snapshot.target.instanceId).toBe("wfi_1");
    expect(events.nextCursor).toBe("opaque-cursor");
    expect(transport.capturedRequests.map((r) => r.method)).toEqual([
      "wrkf.evidence.schema",
      "wrkf.obligation.create",
      "wrkf.supervisor.call",
      "wrkf.supervisor.escalate",
      "wrkf.watch.snapshot",
      "wrkf.watch.events",
    ]);
  });
});
