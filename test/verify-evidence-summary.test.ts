import { afterEach, describe, expect, test } from "bun:test";
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { execFileSync, spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";

type JustRecipe = {
  body: unknown[];
  dependencies: Array<{ recipe: string }>;
  parameters: Array<{ name: string; default: string | null }>;
};

const repoRoot = process.cwd();
const tool = join(repoRoot, "tools", "verify-evidence-summary.mjs");
const scratch: string[] = [];

afterEach(() => {
  for (const dir of scratch.splice(0)) rmSync(dir, { force: true, recursive: true });
});

function dumpedRecipes(): Record<string, JustRecipe> {
  const dump = execFileSync("just", ["--dump", "--dump-format", "json"], {
    cwd: repoRoot,
    encoding: "utf8",
  });

  return JSON.parse(dump).recipes;
}

function bodyText(recipe: JustRecipe): string {
  return recipe.body.map((line) => JSON.stringify(line)).join("\n");
}

function dryRun(recipe: string): string {
  const result = spawnSync("just", ["--dry-run", recipe], {
    cwd: repoRoot,
    encoding: "utf8",
  });
  expect(result.status, result.stderr).toBe(0);
  return `${result.stdout}${result.stderr}`;
}

type EvidenceRecord = { recipe: string; predicate_id: string; exit_code: number; diagnostic: string };

/**
 * Runs the evidence-summary tool with a `just` shim first on PATH, so no real
 * predicate runs: the shim logs each recipe it is asked for, prints a line, and
 * exits 3 for `failing`.
 */
function runSummary(format: string, failing = "lint") {
  const dir = mkdtempSync(join(tmpdir(), "wrkq-verify-evidence-"));
  scratch.push(dir);
  const calls = join(dir, "calls.log");
  writeFileSync(
    join(dir, "just"),
    `#!/bin/sh\necho "$1" >> "${calls}"\necho "ran $1"\n[ "$1" = "${failing}" ] && exit 3\nexit 0\n`,
  );
  chmodSync(join(dir, "just"), 0o755);
  const result = spawnSync("node", [tool, format], {
    cwd: dir,
    encoding: "utf8",
    env: { ...process.env, PATH: `${dir}:${process.env.PATH ?? ""}` },
  });
  const ran = existsSync(calls) ? readFileSync(calls, "utf8").trimEnd().split("\n") : [];
  return { status: result.status, stdout: result.stdout, stderr: result.stderr, ran };
}

describe("verify evidence summary", () => {
  test("default format prints nothing and runs no predicate, so the human verify stays quiet", () => {
    expect(runSummary("")).toEqual({ status: 0, stdout: "", stderr: "", ran: [] });
  });

  test("an unknown format exits 2 with usage and runs no predicate", () => {
    const result = runSummary("yaml");
    expect(result.status).toBe(2);
    expect(result.stderr).toContain("usage: just verify-evidence-summary [json|ndjson]");
    expect(result.ran).toEqual([]);
  });

  test.each(["json", "ndjson"])(
    "%s emits one record per predicate with recipe, predicate_id, exit_code and a diagnostic log, and exits 1 when any predicate failed",
    (format) => {
      const result = runSummary(format);
      const records: EvidenceRecord[] =
        format === "json"
          ? JSON.parse(result.stdout)
          : result.stdout.trimEnd().split("\n").map((line) => JSON.parse(line));

      expect(result.status).toBe(1);
      expect(records.map((record) => record.recipe)).toEqual(result.ran);
      records.forEach((record, index) => {
        const ordinal = String(index + 1).padStart(2, "0");
        expect(record.predicate_id).toBe(`verify.${ordinal}.${record.recipe}`);
        expect(record.exit_code).toBe(record.recipe === "lint" ? 3 : 0);
        expect(readFileSync(record.diagnostic, "utf8")).toBe(`ran ${record.recipe}\n`);
      });
      expect(records.some((record) => record.recipe === "lint")).toBe(true);
    },
  );

  test("the summary covers every predicate the verify recipe gates on, in order", () => {
    const verifyPredicates = dumpedRecipes().verify.dependencies.map((dependency) => dependency.recipe);
    expect(runSummary("ndjson").ran).toEqual(verifyPredicates);
  });

  test("verify accepts an optional summary selector and keeps its human success message", () => {
    const verify = dumpedRecipes().verify;
    expect(verify.parameters).toContainEqual(expect.objectContaining({ name: "summary", default: "" }));
    expect(bodyText(verify)).toContain("verify-evidence-summary");
    expect(bodyText(verify)).toContain("✓ All checks passed");
  });

  test("verify exercises repo-local RPC binaries without installing or publishing", () => {
    const integration = dumpedRecipes()["client-integration"];

    expect(integration, "client-integration must remain part of verify-rpc").toBeDefined();
    expect(integration.dependencies.map((dependency) => dependency.recipe)).toContain("build");

    const integrationBody = bodyText(integration);
    for (const binary of ["WRKQ_BIN", "WRKF_BIN", "WRKQADM_BIN", "WRKQD_BIN"]) {
      expect(integrationBody, `${binary} must resolve from the repo-local build`).toContain(
        `${binary}=\\\"$repo_root/bin/`,
      );
    }

    const plan = dryRun("verify");
    for (const forbidden of [
      "Installing to ~/.local/bin",
      "mkdir -p ~/.local/bin",
      "cp bin/wrkq ~/.local/bin",
      "just client-publish-dev",
      "publish-local-verdaccio.ts",
    ]) {
      expect(plan, `verify plan must not contain deployment action: ${forbidden}`).not.toContain(
        forbidden,
      );
    }
  });
});
