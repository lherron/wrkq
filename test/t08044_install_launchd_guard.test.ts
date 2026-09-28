import { beforeEach, describe, expect, test } from "bun:test";
import { spawnSync } from "node:child_process";
import { copyFileSync, existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// `just install-launchd` / `install-llama-launchd` go through
// scripts/launchd-install.sh (T-08044): absent → install, plist-equivalent →
// no-op, any other difference → refuse unless --force. mini-wrkqd.plist is
// fabricated from mini's captured `launchctl print` (launchctl-print fixture);
// the destination is always a scratch dir, never ~/Library/LaunchAgents.
const repoRoot = process.cwd();
const script = join(repoRoot, "scripts", "launchd-install.sh");
const repoPlist = join(repoRoot, "launchd", "com.praesidium.wrkq-server.plist");
const miniPlist = join(repoRoot, "test", "fixtures", "launchd", "mini-wrkqd.plist");

let dir: string;
let dst: string;
beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "t08044-"));
  dst = join(dir, "LaunchAgents", "com.praesidium.wrkq-server.plist");
});

function install(...extra: string[]) {
  const r = spawnSync("bash", [script, repoPlist, dst, ...extra], { encoding: "utf8" });
  return { status: r.status, out: r.stdout, err: r.stderr };
}

function seed(from: string) {
  install(); // creates the directory
  copyFileSync(from, dst);
}

describe.skipIf(process.platform !== "darwin")("launchd-install guard", () => {
  test("absent destination installs", () => {
    const r = install();
    expect(r.status).toBe(0);
    expect(readFileSync(dst, "utf8")).toBe(readFileSync(repoPlist, "utf8"));
  });

  test("plist-equivalent destination is a no-op", () => {
    seed(repoPlist);
    // Reformat to binary: bytes differ, plist is the same.
    spawnSync("plutil", ["-convert", "binary1", dst]);
    const r = install();
    expect(r.status).toBe(0);
    expect(r.out).toContain("already matches");
  });

  test("mini's canonical wrkqd is refused and left untouched", () => {
    seed(miniPlist);
    const before = readFileSync(dst, "utf8");
    const r = install();
    expect(r.status).toBe(1);
    expect(readFileSync(dst, "utf8")).toBe(before);
    for (const lost of [
      "arg /Users/lherron/.local/bin/wrkqd",
      "arg --node-tokens-file",
      "arg 100.117.215.92:7171",
      "env WRKF_HOOK_CATALOG=",
      "env WRKQ_ATTACH_DIR=/Users/lherron/.praesidium-data/wrkq/attachments  (repo: ",
    ]) {
      expect(r.err).toContain(lost);
    }
    expect(r.err).toContain("Normalized diff (installed → repo)");
    expect(r.err).toContain("--force");
  });

  test("any difference refuses, even a same-program dev edit", () => {
    seed(repoPlist);
    writeFileSync(dst, readFileSync(repoPlist, "utf8").replace("en_US.UTF-8", "C"));
    const r = install();
    expect(r.status).toBe(1);
    expect(r.err).toContain("env LANG=C  (repo: en_US.UTF-8)");
  });

  test("--force overwrites", () => {
    seed(miniPlist);
    const r = install("--force");
    expect(r.status).toBe(0);
    expect(readFileSync(dst, "utf8")).toBe(readFileSync(repoPlist, "utf8"));
  });

  test("unreadable destination refuses; --force overrides", () => {
    seed(repoPlist);
    writeFileSync(dst, "not a plist");
    expect(install().status).toBe(1);
    expect(readFileSync(dst, "utf8")).toBe("not a plist");
    expect(install("--force").status).toBe(0);
  });

  test("unknown flag refuses without touching the destination", () => {
    const r = install("--frce");
    expect(r.status).toBe(2);
    expect(r.err).toContain("unknown flag --frce");
    expect(existsSync(dst)).toBe(false);
  });
});
