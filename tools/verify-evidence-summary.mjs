#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const predicates = [
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
];

const format = process.argv[2] ?? "";

if (format === "") {
  process.exit(0);
}

if (format !== "json" && format !== "ndjson") {
  console.error("usage: just verify-evidence-summary [json|ndjson]");
  process.exit(2);
}

const logDir = join(tmpdir(), `wrkq-verify-evidence-${process.pid}`);
mkdirSync(logDir, { recursive: true });

const records = predicates.map((recipe, index) => {
  const result = spawnSync("just", [recipe], {
    cwd: process.cwd(),
    encoding: "utf8",
    env: process.env,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const exitCode = result.status ?? (result.signal ? 128 : 1);
  const diagnostic = join(logDir, `${String(index + 1).padStart(2, "0")}-${recipe}.log`);
  writeFileSync(diagnostic, `${result.stdout ?? ""}${result.stderr ?? ""}`);

  return {
    recipe,
    predicate_id: `verify.${String(index + 1).padStart(2, "0")}.${recipe}`,
    exit_code: exitCode,
    diagnostic,
  };
});

if (format === "json") {
  console.log(JSON.stringify(records, null, 2));
} else {
  for (const record of records) {
    console.log(JSON.stringify(record));
  }
}

process.exit(records.some((record) => record.exit_code !== 0) ? 1 : 0);
