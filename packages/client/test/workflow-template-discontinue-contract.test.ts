import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = join(import.meta.dir, "..", "..", "..");
const read = (path: string): string => readFileSync(join(repoRoot, path), "utf8");

describe("T-06357 workflow template discontinuation contract", () => {
  test("forward spec documents discontinue/reinstate, attachDiscontinued and discontinuedAt/By", () => {
    // The facade itself (method strings + params) is covered at the public API by
    // wrkf-facade.test.ts; the wire behavior by internal/workrpc
    // TestWorkflowDiscontinueRPCAndAttachOverrideContract. This guards the doc
    // consumers copy from against drift.
    const forwardSpec = read("docs/wrkq-wrkf-rpc-client-forward-spec.md");
    for (const fragment of [
      "wrkf.workflow.discontinue",
      "wrkf.workflow.reinstate",
      "attachDiscontinued?: boolean",
      "discontinuedAt?: string",
      "discontinuedBy?: string",
      "not to the definition hash",
    ]) {
      expect(forwardSpec).toContain(fragment);
    }
  });
});
