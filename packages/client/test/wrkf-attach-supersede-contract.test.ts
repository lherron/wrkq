/**
 * T-05822 red bar.
 *
 * The implementation seam is Go, but this workflow action is configured for the
 * Bun runner. These source-contract assertions keep the test collectible under
 * Bun while pinning the public attach-supersede acceptance bar: explicit
 * predecessor CAS, atomic replace, durable lineage, task-state isolation, and
 * non-success readback for closed/superseded.
 */

import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = join(import.meta.dir, "..", "..", "..");

function readRepoFile(path: string): string {
  return readFileSync(join(repoRoot, path), "utf8");
}

function functionBody(source: string, name: string): string {
  const start = source.indexOf(`func ${name}`);
  expect(start, `${name} must exist`).toBeGreaterThanOrEqual(0);

  const open = source.indexOf("{", start);
  expect(open, `${name} must have a body`).toBeGreaterThanOrEqual(0);

  let depth = 0;
  for (let i = open; i < source.length; i += 1) {
    if (source[i] === "{") depth += 1;
    if (source[i] === "}") depth -= 1;
    if (depth === 0) return source.slice(open + 1, i);
  }

  throw new Error(`${name} body did not terminate`);
}

function typeBlock(source: string, name: string): string {
  const start = source.indexOf(`type ${name} `);
  expect(start, `${name} type must exist`).toBeGreaterThanOrEqual(0);

  const open = source.indexOf("{", start);
  expect(open, `${name} type must have fields`).toBeGreaterThanOrEqual(0);

  let depth = 0;
  for (let i = open; i < source.length; i += 1) {
    if (source[i] === "{") depth += 1;
    if (source[i] === "}") depth -= 1;
    if (depth === 0) return source.slice(start, i + 1);
  }

  throw new Error(`${name} type did not terminate`);
}

function interfaceBlock(source: string, name: string): string {
  const start = source.indexOf(`interface ${name} `);
  expect(start, `${name} interface must exist`).toBeGreaterThanOrEqual(0);

  const open = source.indexOf("{", start);
  expect(open, `${name} interface must have fields`).toBeGreaterThanOrEqual(0);

  let depth = 0;
  for (let i = open; i < source.length; i += 1) {
    if (source[i] === "{") depth += 1;
    if (source[i] === "}") depth -= 1;
    if (depth === 0) return source.slice(start, i + 1);
  }

  throw new Error(`${name} interface did not terminate`);
}

describe("T-05822 wrkf attach supersede contract", () => {
  test("public attach params expose explicit supersede with predecessor CAS guard", () => {
    const goParams = typeBlock(readRepoFile("internal/wrkqapi/types.go"), "WorkflowAttachParams");
    const tsParams = interfaceBlock(
      readRepoFile("packages/client/src/wrkq/types.ts"),
      "WrkqWorkflowAttachParams",
    );
    const cli = readRepoFile("internal/wrkfcli/root.go");

    for (const [surface, source] of [
      ["Go RPC WorkflowAttachParams", goParams],
      ["TypeScript WrkqWorkflowAttachParams", tsParams],
    ] as const) {
      expect(source, `${surface} must carry an explicit supersede opt-in`).toMatch(
        /Supersede|supersede/,
      );
      expect(
        source,
        `${surface} must require the caller to name the live predecessor instance`,
      ).toMatch(/PredecessorInstance|predecessorInstance|expectInstance|expectCurrentInstance/);
      expect(
        source,
        `${surface} must require a predecessor revision/current-generation CAS token`,
      ).toMatch(/PredecessorRevision|predecessorRevision|expectRevision|expectCurrentRevision/);
    }

    expect(cli, "wrkf task attach must expose --supersede CLI sugar").toContain("--supersede");
    expect(
      cli,
      "wrkf task attach --supersede must expose predecessor/current-generation CAS",
    ).toMatch(/predecessor|expect-current|expect-revision|expect-instance/i);
  });

  test("supersede transaction closes the matched predecessor before inserting the successor", () => {
    const schema = readRepoFile("schema_dump.sql");
    expect(schema, "one live workflow instance per task remains enforced by the partial unique index").toContain(
      "workflow_instances_one_active_per_task",
    );
    expect(schema, "the uniqueness guard must remain scoped to non-closed instances").toContain(
      "WHERE status != 'closed'",
    );

    const workflowAttachBody = functionBody(
      readRepoFile("internal/wrkqapi/workflow.go"),
      "(a *API) WorkflowAttach",
    );
    const service = readRepoFile("internal/workflow/service.go");

    expect(
      workflowAttachBody,
      "wrkq.workflow.attach must pass supersede/predecessor CAS options into workflow attachment",
    ).toMatch(/Supersede|Predecessor|Expect(Current)?/);

    expect(
      service,
      "workflow service should expose an option-bearing attach path, not only AttachTask(task, workflow, actor)",
    ).toMatch(/AttachTaskOptions|TaskAttachOptions|AttachWorkflowOptions|Supersede/);

    const attachBody = functionBody(service, "(s *Service) AttachTask");
    expect(
      attachBody,
      "supersede must close the predecessor as closed/superseded inside the attach transaction",
    ).toMatch(/UPDATE\s+workflow_instances[\s\S]+status\s*=\s*['"]closed['"][\s\S]+phase\s*=\s*['"]superseded['"]/);
    expect(
      attachBody,
      "the stale predecessor guard must compare the current live instance id and revision",
    ).toMatch(/predecessor|expect.*instance|revision/i);
  });

  test("supersede records event lineage and does not use cancellation task-state effects", () => {
    const service = readRepoFile("internal/workflow/service.go");
    const attachBody = functionBody(service, "(s *Service) AttachTask");

    expect(attachBody, "predecessor must receive workflow.superseded lineage event").toContain(
      "workflow.superseded",
    );
    expect(attachBody, "successor workflow.attached payload must name superseded predecessor").toMatch(
      /superseded|predecessor/i,
    );
    expect(
      attachBody,
      "supersede is engine-owned replacement and must not synthesize set_task_state effects",
    ).not.toContain("set_task_state");
    expect(
      attachBody,
      "supersede must not drive the operator_resolved/cancelled lane",
    ).not.toContain("operator_resolved");
  });

  test("plain cancellation remains a separate terminal lane that writes task state cancelled", () => {
    const v3 = readRepoFile("internal/workflow/builtins/wrkq-simple-task-v3.workflow.json");

    expect(v3, "negative guard: plain operator resolution still has a cancelled outcome").toContain(
      '"operator_resolved"',
    );
    expect(v3, "negative guard: plain cancel remains closed/cancelled, not superseded").toContain(
      '"phase": "cancelled"',
    );
    expect(v3, "negative guard: plain cancel still propagates task state cancelled").toContain(
      '"kind": "set_task_state"',
    );
    expect(v3, "negative guard: plain cancel set_task_state payload is still cancelled").toContain(
      '"state": "cancelled"',
    );
  });

  test("closed superseded instances are classified as non-success by watch/readback consumers", () => {
    const watch = readRepoFile("internal/workflow/watch.go");
    const classifier = functionBody(watch, "classifyInstanceTerminal");

    expect(
      classifier,
      "closed/superseded must be explicitly classified before the success fallback",
    ).toContain("superseded");
    expect(
      classifier,
      "closed/superseded must not report success exit 0",
    ).toMatch(/superseded[\s\S]+WatchClass(?:Cancelled|Failure)|superseded[\s\S]+return\s+[^,\n]+,\s*(?:10|11)/);
  });
});
