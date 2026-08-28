/**
 * T-05619 red bar.
 *
 * This intentionally pins the bounded refactor contract from the outside of the
 * Go package: one canonical transition decision layer must exist and the three
 * current transition legality surfaces must delegate to it. The assertions are
 * source-contract checks so they collect under the configured Bun runner while
 * failing as test-case assertions on the pre-refactor tree.
 */

import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
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

describe("T-05619 canonical wrkf transition decision contract", () => {
  test("defines one side-effect-free TransitionDecision evaluator API", () => {
    const decisionPath = "internal/workflow/transition_decision.go";
    const decisionExists = existsSync(join(repoRoot, decisionPath));
    expect(decisionExists, `${decisionPath} should hold the canonical evaluator`).toBe(true);

    const source = decisionExists ? readRepoFile(decisionPath) : "";
    // The decision types live beside the evaluator (T-07619): the contract is
    // one evaluator API, not one file.
    const typesPath = "internal/workflow/types_transition_decision.go";
    const declarations = existsSync(join(repoRoot, typesPath)) ? readRepoFile(typesPath) : source;
    expect(declarations).toContain("type TransitionDecisionInput");
    expect(declarations).toContain("type TransitionDecision");
    expect(declarations).toContain("type TransitionFollowUp");
    expect(source).toMatch(/func\s+\(s \*Service\)\s+EvaluateTransitionDecision\s*\(/);

    const body = functionBody(source, "(s *Service) EvaluateTransitionDecision");
    for (const forbidden of [
      "executeCheck(",
      "AddEvidence(",
      "insertTransitionEvent",
      "updateTaskWorkflowMeta",
      "CompleteAction(",
      "FinishWorkflowRun",
    ]) {
      expect(body, `decision evaluator must not perform side effects via ${forbidden}`).not.toContain(forbidden);
    }
  });

  test("Next, direct transition, and action settlement all delegate legality to the evaluator", () => {
    const service = readRepoFile("internal/workflow/service.go");
    const ledger = readRepoFile("internal/workflow/ledger.go");
    const action = readRepoFile("internal/workflow/action.go");

    const nextBody = functionBody(service, "(s *Service) Next");
    const directBody = functionBody(ledger, "(s *Service) TransitionForSelectors");
    const settleBody = functionBody(action, "(s *Service) applyActionTransitionTx");

    for (const [surface, body] of [
      ["Service.Next", nextBody],
      ["TransitionForSelectors", directBody],
      ["applyActionTransitionTx", settleBody],
    ] as const) {
      expect(body, `${surface} should use the canonical transition decision evaluator`).toContain(
        "EvaluateTransitionDecision",
      );
    }

    const duplicatedLegalityHelpers = [
      "transitionBlockers(",
      "checkCommitBlockers(",
      "separationOfDutyBlockers(",
      "postconditionBlockers(",
    ];

    for (const [surface, body] of [
      ["Service.Next", nextBody],
      ["TransitionForSelectors", directBody],
      ["applyActionTransitionTx", settleBody],
    ] as const) {
      for (const helper of duplicatedLegalityHelpers) {
        expect(body, `${surface} should not duplicate evaluator-owned ${helper} legality`).not.toContain(helper);
      }
    }
  });
});
