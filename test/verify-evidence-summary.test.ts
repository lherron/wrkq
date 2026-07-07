import { describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";

type JustRecipe = {
  body: unknown[];
  dependencies: Array<{ recipe: string }>;
  parameters: Array<{ name: string; default: string | null }>;
};

function dumpedRecipes(): Record<string, JustRecipe> {
  const dump = execFileSync("just", ["--dump", "--dump-format", "json"], {
    cwd: process.cwd(),
    encoding: "utf8",
  });

  return JSON.parse(dump).recipes;
}

function bodyText(recipe: JustRecipe): string {
  return recipe.body.map((line) => JSON.stringify(line)).join("\n");
}

describe("verify evidence summary recipe contract", () => {
  test("verify keeps the human gate intact while exposing machine-readable evidence predicates", () => {
    const recipes = dumpedRecipes();
    const verify = recipes.verify;

    // T-05798 is a Justfile legibility change: inspect the recipe contract without
    // executing the full verification gate or depending on unrelated lint health.
    expect(verify, "verify recipe must remain the public verification gate").toBeDefined();
    expect(
      verify.dependencies.map((dependency) => dependency.recipe),
      "verify must keep the existing human predicate order unchanged",
    ).toEqual([
      "fitkit-s6",
      "suppression-lint",
      "layer-boundary",
      "lint",
      "test",
      "rot-sensor",
      "surface-guard",
      "doc-links",
      "architecture-records",
      "verify-rpc",
    ]);
    expect(
      bodyText(verify),
      "verify must keep the human success message unchanged",
    ).toContain("✓ All checks passed");

    expect(
      verify.parameters,
      "verify must accept an optional summary selector for json or ndjson evidence output",
    ).toContainEqual(expect.objectContaining({ name: "summary", default: "" }));

    const summary = recipes["verify-evidence-summary"];
    expect(
      summary,
      "verify must expose a bounded evidence-summary helper instead of hiding predicate evidence in the human recipe",
    ).toBeDefined();
    expect(
      summary.parameters,
      "verify-evidence-summary must default to no output so the regular human verify stays quiet",
    ).toContainEqual(expect.objectContaining({ name: "format", default: "" }));

    const summaryBody = bodyText(summary);
    for (const token of ["json", "ndjson", "recipe", "predicate_id", "exit_code", "diagnostic"]) {
      expect(
        summaryBody,
        `verify-evidence-summary must describe ${token} for per-predicate triage output`,
      ).toContain(token);
    }
  });
});
