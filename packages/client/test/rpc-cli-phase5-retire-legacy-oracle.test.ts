/**
 * T-05621 red bar.
 *
 * Phase 5 retires the day-to-day legacy oracle. This source-contract test keeps
 * the bar bounded for the configured Bun runner while asserting only observable
 * repository surfaces: active oracle command entrypoints, the build target that
 * compiles them, and the parity harness that treats the legacy CLI as oracle.
 */

import { describe, expect, test } from "bun:test";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const repoRoot = join(import.meta.dir, "..", "..", "..");

function repoPath(path: string): string {
  return join(repoRoot, path);
}

function readRepoFile(path: string): string {
  return readFileSync(repoPath(path), "utf8");
}

function listFilesUnder(path: string): string[] {
  const root = repoPath(path);
  if (!existsSync(root)) return [];

  const files: string[] = [];
  const visit = (current: string) => {
    for (const entry of readdirSync(current)) {
      const full = join(current, entry);
      if (statSync(full).isDirectory()) {
        visit(full);
      } else {
        files.push(relative(repoRoot, full));
      }
    }
  };

  visit(root);
  return files.sort();
}

function isExplicitTestOnlyQuarantine(path: string): boolean {
  if (!existsSync(repoPath(path))) return true;

  const combinedSource = listFilesUnder(path)
    .filter((file) => file.endsWith(".go") || file.endsWith(".md"))
    .map((file) => readRepoFile(file))
    .join("\n");

  return (
    combinedSource.includes("//go:build wrkq_legacy_oracle_quarantine") &&
    combinedSource.includes("test-only quarantine") &&
    combinedSource.includes("sunset")
  );
}

describe("T-05621 Phase 5 retires active legacy oracle surfaces", () => {
  test("legacy wrkq oracle entrypoints are deleted or placed behind an explicit test-only quarantine", () => {
    for (const commandDir of ["cmd/wrkq-legacy", "cmd/wrkq-rpccli"]) {
      expect(
        isExplicitTestOnlyQuarantine(commandDir),
        `${commandDir} must be deleted, or every retained Go entrypoint must be behind //go:build wrkq_legacy_oracle_quarantine with a test-only quarantine sunset note`,
      ).toBe(true);
    }
  });

  test("default build plumbing no longer exposes the oracle binaries", () => {
    const justfile = readRepoFile("Justfile");

    expect(justfile, "Justfile must not expose the retired build-rpc-oracle recipe").not.toMatch(
      /^build-rpc-oracle:/m,
    );
    expect(justfile, "Justfile must not build cmd/wrkq-legacy in any default recipe").not.toContain(
      "./cmd/wrkq-legacy",
    );
    expect(justfile, "Justfile must not build cmd/wrkq-rpccli in any default recipe").not.toContain(
      "./cmd/wrkq-rpccli",
    );
  });

  test("rpccli contract tests do not compile the old-vs-new parity oracle", () => {
    const rpccliTests = listFilesUnder("internal/rpccli").filter((file) => file.endsWith("_test.go"));
    const activeOracleReferences = rpccliTests.flatMap((file) => {
      const source = readRepoFile(file);
      const forbidden = ["buildParityBinaries", "./cmd/wrkq-legacy", "./cmd/wrkq-rpccli", "wrkq-legacy"];
      return forbidden
        .filter((needle) => source.includes(needle))
        .map((needle) => `${file} -> ${needle}`);
    });

    expect(
      activeOracleReferences,
      "retained rpccli coverage must assert production RPC-backed contracts directly, not build or compare against the legacy oracle",
    ).toEqual([]);
  });
});
