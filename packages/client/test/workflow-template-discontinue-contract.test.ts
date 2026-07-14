import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = join(import.meta.dir, "..", "..", "..");
const read = (path: string): string => readFileSync(join(repoRoot, path), "utf8");

describe("T-06357 workflow template discontinuation contract", () => {
  test("RPC ownership, params, results, and catalogs are frozen", () => {
    const registry = read("internal/workrpc/registry.go");
    const apiTypes = read("internal/wrkfapi/types.go");
    const schema = read("internal/workrpc/schema.go");

    for (const method of ["wrkf.workflow.discontinue", "wrkf.workflow.reinstate"]) {
      expect(registry).toContain(`s.Register("${method}"`);
      expect(registry).toContain(`"${method}"`);
    }
    expect(registry).toMatch(
      /type templateLifecycleParams struct \{[\s\S]*Ref\s+string\s+`json:"ref"`[\s\S]*PrincipalRef\s+string\s+`json:"principal_ref,omitempty"`/,
    );
    expect(apiTypes).toMatch(
      /type WorkflowShowResult struct \{[\s\S]*DiscontinuedAt\s+string\s+`json:"discontinuedAt,omitempty"`[\s\S]*DiscontinuedBy\s+string\s+`json:"discontinuedBy,omitempty"`/,
    );
    expect(apiTypes).toMatch(
      /type TemplateSummary struct \{[\s\S]*DiscontinuedAt\s+string\s+`json:"discontinuedAt,omitempty"`[\s\S]*DiscontinuedBy\s+string\s+`json:"discontinuedBy,omitempty"`/,
    );
    expect(schema).toContain('"WrkfWorkflowTemplateSummary"');
    expect(schema).toContain('"WrkfWorkflowShowResult"');
    expect(registry).not.toContain('s.Register("wrkf.workflow.attach"');
    expect(registry).not.toContain('s.Register("wrkf.task.attach"');
  });

  test("attach override crosses only the wrkq producer and client facade", () => {
    const goParams = read("internal/wrkqapi/types.go");
    const producer = read("internal/wrkqapi/workflow.go");
    const tsTypes = read("packages/client/src/wrkq/types.ts");

    expect(goParams).toMatch(/AttachDiscontinued\s+bool\s+`json:"attachDiscontinued,omitempty"`/);
    expect(producer).toContain("AttachDiscontinued:    p.AttachDiscontinued");
    expect(tsTypes).toContain("attachDiscontinued?: boolean;");
  });

  test("client lifecycle facade and forward spec match the wire", () => {
    const client = read("packages/client/src/client.ts");
    const facade = read("packages/client/src/wrkf/facade.ts");
    const types = read("packages/client/src/wrkf/types.ts");
    const forwardSpec = read("docs/wrkq-wrkf-rpc-client-forward-spec.md");

    expect(client).toContain('this.call("wrkf.workflow.discontinue", p)');
    expect(client).toContain('this.call("wrkf.workflow.reinstate", p)');
    expect(facade).toContain(
      "discontinue(params: WrkfWorkflowLifecycleParams): Promise<WrkfWorkflowShowResult>",
    );
    expect(facade).toContain(
      "reinstate(params: WrkfWorkflowLifecycleParams): Promise<WrkfWorkflowShowResult>",
    );
    expect(types).toMatch(
      /interface WrkfWorkflowLifecycleParams \{[\s\S]*ref: string;[\s\S]*principal_ref\?: string;/,
    );
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
