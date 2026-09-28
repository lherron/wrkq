import { describe, expect, test } from "bun:test";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";

// `just install` resolves the launchd job's database through
// scripts/resolve-job-db.sh (T-08927). mini-wrkq-server-db-arg.txt is mini's
// real `launchctl print` of its wrkqd job (--db argument, no WRKQ_DB_PATH in
// the job env); max3-llama-server.txt is a real capture of a non-wrkq job;
// repo-plist-wrkq-server-env.txt is synthesized in that shape from
// launchd/com.praesidium.wrkq-server.plist.
const repoRoot = process.cwd();
const resolver = join(repoRoot, "scripts", "resolve-job-db.sh");
const fixtures = join(repoRoot, "test", "fixtures", "launchctl-print");
const fixture = (name: string) => readFileSync(join(fixtures, name), "utf8");
const mini = fixture("mini-wrkq-server-db-arg.txt");
const miniDB = "/Users/lherron/.praesidium-data/wrkq/wrkq.db";
const stubDB = "/Users/lherron/praesidium/var/db/wrkq.db";

function resolve(print: string) {
  // The caller's shell names the stub, as mini's does: it must never be read.
  const r = spawnSync("bash", [resolver], {
    input: print,
    encoding: "utf8",
    env: { ...process.env, WRKQ_DB_PATH: stubDB, WRKQ_DB: "rpc://127.0.0.1:1" },
  });
  return { status: r.status, out: r.stdout.trim(), err: r.stderr.trim() };
}

function withJobEnv(print: string, lines: string[]): string {
  return print.replace("\tenvironment = {\n", `\tenvironment = {\n${lines.map((l) => `\t\t${l}\n`).join("")}`);
}

const miniNoDbArg = mini.replace("\t\t--db\n\t\t/Users/lherron/.praesidium-data/wrkq/wrkq.db\n", "");

describe("resolve-job-db", () => {
  test("mini: --db program argument wins; shell WRKQ_DB_PATH ignored", () => {
    expect(resolve(mini)).toEqual({ status: 0, out: miniDB, err: "" });
  });

  test("inherited-environment WRKQ_DB_PATH is not the job's environment", () => {
    const inheritedStub = miniNoDbArg.replace(
      "\tinherited environment = {\n",
      `\tinherited environment = {\n\t\tWRKQ_DB_PATH => ${stubDB}\n`,
    );
    const r = resolve(inheritedStub);
    expect(r.status).toBe(1);
    expect(r.err).toContain("no --db argument");
  });

  test("repo plist: job environment WRKQ_DB_PATH", () => {
    expect(resolve(fixture("repo-plist-wrkq-server-env.txt")).out).toBe(stubDB);
  });

  test.each([["--db=/x/a.db"], ["-db=/x/a.db"]])("Go flag form %s", (arg) => {
    const print = mini.replace("\t\t--db\n\t\t/Users/lherron/.praesidium-data/wrkq/wrkq.db\n", `\t\t${arg}\n`);
    expect(resolve(print).out).toBe("/x/a.db");
  });

  test("single-dash -db X", () => {
    expect(resolve(mini.replace("\t\t--db\n", "\t\t-db\n")).out).toBe(miniDB);
  });

  test("--db argument outranks job environment", () => {
    expect(resolve(withJobEnv(mini, ["WRKQ_DB_PATH => /other.db"])).out).toBe(miniDB);
  });

  test("job WRKQ_DB local path", () => {
    expect(resolve(withJobEnv(miniNoDbArg, ["WRKQ_DB => /x/b.db"])).out).toBe("/x/b.db");
  });

  test("job WRKQ_DB agreeing with WRKQ_DB_PATH", () => {
    expect(resolve(withJobEnv(miniNoDbArg, ["WRKQ_DB => /x/b.db", "WRKQ_DB_PATH => /x/b.db"])).out).toBe("/x/b.db");
  });

  test.each([
    ["no locator anywhere in the job", miniNoDbArg, "no --db argument"],
    ["real max3 capture of a non-wrkq job", fixture("max3-llama-server.txt"), "no --db argument"],
    ["remote WRKQ_DB", withJobEnv(miniNoDbArg, ["WRKQ_DB => rpc://mini:7171"]), "remote locator"],
    ["WRKQ_DB vs WRKQ_DB_PATH conflict", withJobEnv(miniNoDbArg, ["WRKQ_DB => /a.db", "WRKQ_DB_PATH => /b.db"]), "T-08302"],
    ["only WRKQ_DB_PATH_FILE", withJobEnv(miniNoDbArg, ["WRKQ_DB_PATH_FILE => /p"]), "WRKQ_DB_PATH_FILE"],
    ["bare trailing --db", mini.replace("\t\t/Users/lherron/.praesidium-data/wrkq/wrkq.db\n\t\t--node-tokens-file\n\t\t/Users/lherron/.config/wrkq/node-tokens\n", ""), "bare --db"],
    ["empty print (job not loaded)", "", "empty launchctl print"],
  ])("refuses: %s", (_name, print, reason) => {
    const r = resolve(print);
    expect(r.status).toBe(1);
    expect(r.out).toBe("");
    expect(r.err).toContain(reason);
  });
});
