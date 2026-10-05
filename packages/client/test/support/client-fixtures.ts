/**
 * client-fixtures.ts — shared builders for the @wrkq/client unit tests that run
 * over the in-memory FakeTransport.
 */

import { createClient } from "../../src/client";
import type { WorkRpcError } from "../../src/errors";
import type { FakeTransport } from "../../src/testing/fake-transport";
import type { WrkqContainer } from "../../src/wrkq/container";
import type { WrkqTask } from "../../src/wrkq/task";

export async function clientWith(transport: FakeTransport, autoInitialize = false) {
  return createClient({ transport, autoInitialize });
}

/** Awaits a call that must reject and returns its error; fails if it resolves. */
export async function rejectionOf(call: Promise<unknown>): Promise<WorkRpcError> {
  try {
    await call;
  } catch (error) {
    return error as WorkRpcError;
  }
  throw new Error("expected the call to reject");
}

export const MOCK_TASK: WrkqTask = {
  openSubtaskCount: 0,
  uuid: "u-1",
  id: "T-00001",
  slug: "my-task",
  title: "my task",
  projectUuid: "p-1",
  path: "inbox/my-task",
  state: "open",
  priority: 3,
  kind: "task",
  description: "",
  specification: "",
  labels: [],
  meta: {},
  riskClass: "medium",
  etag: 2,
  assigneePrincipalRef: "agent:larry",
  requesterPrincipalRef: "agent:mable",
  requesterScopeRef: "agent:mable:project:wrkq:role:primary",
  createdAt: "2026-06-30T00:00:00Z",
  updatedAt: "2026-06-30T00:00:00Z",
};

export const MOCK_CONTAINER: WrkqContainer = {
  uuid: "p-1",
  id: "P-00001",
  slug: "project",
  title: "Project",
  description: "",
  labels: [],
  kind: "project",
  path: "project",
  etag: 1,
  createdAt: "2026-06-30T00:00:00Z",
  updatedAt: "2026-06-30T00:00:00Z",
};
