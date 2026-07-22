/**
 * T-05371 red bar: the old Control Plane task linkage family is no longer a
 * live wrkq task contract. Historical migrations and DB-only schema artifacts
 * may keep the storage columns until T-04317; public/client/RPC/CLI/webhook
 * surfaces must not expose or accept them.
 */

import { describe, expect, test } from "bun:test";
import { readdir, readFile } from "node:fs/promises";
import { join, relative, sep } from "node:path";

const REPO_ROOT = join(import.meta.dir, "../../..");

const LIVE_SURFACE_ROOTS = [
  "internal",
  "docs",
  "cap",
  "packages/client",
  "mcp-server",
  "test",
];

const ALWAYS_LEGACY_FIELD_PATTERNS = [
  /\bcp_project_id\b/g,
  /\bcp_work_item_id\b/g,
  /\bcp_run_id\b/g,
  /\bcp_session_id\b/g,
  /\bcpProjectId\b/g,
  /\bcpWorkItemId\b/g,
  /\bcpRunId\b/g,
];

const TASK_CONTRACT_FIELD_PATTERNS = [
  /\brun_status\b/g,
  /\bsessionId\b/g,
  /\brunStatus\b/g,
];

const TASK_CONTRACT_PATH_PREFIXES = [
  "internal/rpccli/",
  "internal/wrkqapi/",
  "internal/store/",
  "internal/domain/",
  "internal/webhooks/",
  "docs/",
  "cap/",
  "packages/client/",
  "test/",
];

const ALLOWED_PATH_SEGMENTS = [
  ["internal", "db", "migrations"],
  ["internal", "surfaceguard", "testdata"],
];

const ALLOWED_FILES = new Set([
  "schema_dump.sql",
  "packages/client/test/legacy-task-linkage-contract.test.ts",
]);

const SCANNED_EXTENSIONS = new Set([
  ".go",
  ".ts",
  ".tsx",
  ".js",
  ".json",
  ".yaml",
  ".yml",
  ".md",
  ".html",
  ".sh",
]);

function isAllowedPath(path: string): boolean {
  if (ALLOWED_FILES.has(path)) return true;
  if (path.endsWith("_test.go") || path.startsWith(`test${sep}`)) return true;
  const parts = path.split(sep);
  return ALLOWED_PATH_SEGMENTS.some((segments) =>
    segments.every((segment, index) => parts[index] === segment),
  );
}

function hasScannedExtension(path: string): boolean {
  return [...SCANNED_EXTENSIONS].some((extension) => path.endsWith(extension));
}

function taskContractPatternsFor(path: string): RegExp[] {
  if (!TASK_CONTRACT_PATH_PREFIXES.some((prefix) => path.startsWith(prefix))) return [];
  return TASK_CONTRACT_FIELD_PATTERNS;
}

async function* walk(dir: string): AsyncGenerator<string> {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    if (entry.name === "node_modules" || entry.name === "dist") continue;
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      yield* walk(path);
    } else if (entry.isFile()) {
      yield path;
    }
  }
}

describe("T-05371 legacy task linkage public surface", () => {
  test("live surfaces do not expose CP task linkage fields", async () => {
    const violations: string[] = [];

    for (const root of LIVE_SURFACE_ROOTS) {
      for await (const file of walk(join(REPO_ROOT, root))) {
        const repoPath = relative(REPO_ROOT, file);
        if (isAllowedPath(repoPath) || !hasScannedExtension(repoPath)) continue;

        const body = await readFile(file, "utf8");
        for (const pattern of [
          ...ALWAYS_LEGACY_FIELD_PATTERNS,
          ...taskContractPatternsFor(repoPath),
        ]) {
          pattern.lastIndex = 0;
          if (pattern.test(body)) {
            violations.push(`${repoPath}: ${pattern.source}`);
          }
        }
      }
    }

    expect(violations).toEqual([]);
  });
});
