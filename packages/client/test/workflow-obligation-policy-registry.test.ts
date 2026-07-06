/**
 * T-05620 red bar.
 *
 * The workflow core must stop embedding simple-task evidence and obligation
 * rules in generic insertion/listing paths. This source-contract test runs
 * under the configured Bun unit runner and fails as assertions on the current
 * tree until the behavior is moved behind a workflow policy/provider boundary.
 */

import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = join(import.meta.dir, "..", "..", "..");

const SIMPLE_TASK_EVIDENCE_KINDS = [
  "delegated_task_manifest",
  "coordinator_runbook",
  "completion_claim",
  "observer_completion_review",
  "operator_resolution",
] as const;

const SIMPLE_TASK_OBLIGATION_KINDS = [
  "await_subordinate_closure",
  "await_coordinator_smoke_execution",
  "await_observer_completion_review",
] as const;

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

describe("T-05620 workflow obligation policy registry boundary", () => {
  test("generic evidence insertion delegates validation and side effects through a workflow policy", () => {
    const policyPath = "internal/workflow/policy.go";
    expect(existsSync(join(repoRoot, policyPath)), `${policyPath} should define the policy registry boundary`).toBe(true);

    const serviceBody = functionBody(readRepoFile("internal/workflow/service.go"), "(s *Service) AddEvidence");
    const actionBody = functionBody(readRepoFile("internal/workflow/action.go"), "(s *Service) addActionEvidenceTx");

    for (const [surface, body] of [
      ["Service.AddEvidence", serviceBody],
      ["addActionEvidenceTx", actionBody],
    ] as const) {
      expect(body, `${surface} should resolve the workflow policy for the active template`).toMatch(
        /WorkflowPolicy|ResolveWorkflowPolicy|workflowPolicyFor/,
      );
      expect(body, `${surface} should validate evidence through the policy before inserting`).toContain(
        "ValidateEvidence",
      );

      for (const kind of SIMPLE_TASK_EVIDENCE_KINDS) {
        expect(body, `${surface} must not branch directly on simple-task evidence kind ${kind}`).not.toContain(
          `params.Kind == "${kind}"`,
        );
      }
    }

    expect(
      serviceBody,
      "Service.AddEvidence should run policy side effects after insert and before context refresh",
    ).toContain("OnEvidenceAdded");
  });

  test("generic obligation listing delegates computed state and projections through the policy", () => {
    const ledgerBody = functionBody(readRepoFile("internal/workflow/ledger.go"), "(s *Service) ListObligations");

    expect(ledgerBody, "ListObligations should resolve the workflow policy for the active template").toMatch(
      /WorkflowPolicy|ResolveWorkflowPolicy|workflowPolicyFor/,
    );
    expect(ledgerBody, "ListObligations should delegate computed obligation projection").toContain(
      "ProjectObligations",
    );

    for (const kind of [...SIMPLE_TASK_EVIDENCE_KINDS, ...SIMPLE_TASK_OBLIGATION_KINDS]) {
      expect(ledgerBody, `ListObligations must not embed simple-task kind ${kind}`).not.toContain(kind);
    }
  });
});
