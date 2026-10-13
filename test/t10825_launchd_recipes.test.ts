import { beforeEach, describe, expect, test } from "bun:test";
import { spawnSync } from "node:child_process";
import {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// `just launchd-install` / `launchd-uninstall` (global/launchd-jobs §2, T-10825)
// run for real against an isolated HOME with stub launchctl and praesidium-adopt
// on PATH. Nothing here touches ~/Library/LaunchAgents or a real launchd domain:
// the stub launchctl only logs its argv, and fails bootstrap on demand.
const repoRoot = process.cwd();
const repoLaunchd = join(repoRoot, "launchd");
const jobs = ["com.praesidium.llama-server", "com.praesidium.wrkq-server"];
const miniPlist = join(repoRoot, "test", "fixtures", "launchd", "mini-wrkqd.plist");
const justBin = spawnSync("bash", ["-lc", "command -v just"], { encoding: "utf8" }).stdout.trim() || "just";

let home: string;
let stubs: string;
let log: string;
let agents: string;
let shim: string;

beforeEach(() => {
  home = mkdtempSync(join(tmpdir(), "t10825-"));
  stubs = join(home, "stubs");
  log = join(home, "calls.log");
  agents = join(home, "Library", "LaunchAgents");
  shim = join(home, ".local", "bin", "praesidium-launch");
  mkdirSync(stubs, { recursive: true });
  writeFileSync(log, "");
  // launchctl: log every call; bootstrap fails (error 5) while its counter
  // is below LAUNCHCTL_BOOTSTRAP_FAILS; bootout of an absent job fails.
  stub(
    "launchctl",
    `echo "launchctl $*" >> "${log}"
case "$1" in
  bootstrap)
    n=$(cat "${home}/bootstrap.count" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "${home}/bootstrap.count"
    if [ "$n" -le "\${LAUNCHCTL_BOOTSTRAP_FAILS:-0}" ]; then echo "Bootstrap failed: 5: Input/output error" >&2; exit 5; fi ;;
  bootout) exit 3 ;;
esac
exit 0`,
  );
});

function stub(name: string, body: string) {
  const p = join(stubs, name);
  writeFileSync(p, `#!/bin/bash\n${body}\n`);
  chmodSync(p, 0o755);
}

// The adopter's two edits (macos/adopt-agent), idempotent, logged.
function stubAdopter(extra = "") {
  stub(
    "praesidium-adopt",
    `echo "praesidium-adopt $*" >> "${log}"
${extra}
f="$1"
if ! plutil -extract AssociatedBundleIdentifiers raw "$f" >/dev/null 2>&1; then
  plutil -insert AssociatedBundleIdentifiers -json '["com.praesidium.AgentHost"]' "$f"
fi
if [ "$(plutil -extract ProgramArguments.0 raw "$f")" != "${shim}" ]; then
  plutil -insert ProgramArguments.0 -string "${shim}" "$f"
fi`,
  );
}

function just(args: string[], env: Record<string, string> = {}) {
  const r = spawnSync(justBin, ["--justfile", join(repoRoot, "Justfile"), "--working-directory", repoRoot, ...args], {
    encoding: "utf8",
    env: {
      HOME: home,
      // No ~/.local/bin: the only adopter is the stub, when a test installs one.
      PATH: `${stubs}:/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin`,
      LAUNCHD_BOOTSTRAP_RETRY_SLEEP: "0",
      ...env,
    },
  });
  return { status: r.status, out: r.stdout, err: r.stderr, calls: readFileSync(log, "utf8").trim().split("\n").filter(Boolean) };
}

const dst = (label: string) => join(agents, `${label}.plist`);
const json = (p: string) => JSON.parse(spawnSync("plutil", ["-convert", "json", "-o", "-", p], { encoding: "utf8" }).stdout);
const uid = spawnSync("id", ["-u"], { encoding: "utf8" }).stdout.trim();

describe.skipIf(process.platform !== "darwin")("launchd-install / launchd-uninstall", () => {
  test("fresh install copies, adopts, then bootout + bootstrap each job in order", () => {
    stubAdopter();
    const r = just(["launchd-install"]);
    expect(r.status).toBe(0);
    const expected: string[] = [];
    for (const label of jobs) {
      const d = dst(label);
      expected.push(`praesidium-adopt ${d}`, `launchctl bootout gui/${uid} ${d}`, `launchctl bootstrap gui/${uid} ${d}`);
      const repo = json(join(repoLaunchd, `${label}.plist`));
      const got = json(d);
      expect(got.AssociatedBundleIdentifiers).toEqual(["com.praesidium.AgentHost"]);
      expect(got.ProgramArguments).toEqual([shim, ...repo.ProgramArguments]);
    }
    expect(r.calls).toEqual(expected);
    expect(r.calls.some((c) => c.includes("kickstart"))).toBe(false);
  });

  test("without an adopter on the host the step is skipped and the copy stays host-neutral", () => {
    const r = just(["launchd-install"]);
    expect(r.status).toBe(0);
    for (const label of jobs) {
      expect(readFileSync(dst(label), "utf8")).toBe(readFileSync(join(repoLaunchd, `${label}.plist`), "utf8"));
    }
    expect(r.calls.filter((c) => c.startsWith("launchctl bootstrap")).length).toBe(2);
  });

  test("reinstall over an adopted copy matches (no refusal) and keeps it adopted", () => {
    stubAdopter();
    expect(just(["launchd-install"]).status).toBe(0);
    const r = just(["launchd-install"]);
    expect(r.status).toBe(0);
    expect(r.out).toContain("already matches");
    expect(json(dst(jobs[0])).ProgramArguments[0]).toBe(shim);
  });

  test("an adopted copy with ONE extra env key is a difference and refuses", () => {
    stubAdopter();
    expect(just(["launchd-install"]).status).toBe(0);
    const d = dst(jobs[0]);
    spawnSync("plutil", ["-insert", "EnvironmentVariables.EXTRA_KEY", "-string", "1", d]);
    const before = readFileSync(d, "utf8");
    writeFileSync(log, "");
    const r = just(["launchd-install"]);
    expect(r.status).not.toBe(0);
    expect(r.err).toContain("NOT installing");
    expect(r.err).toContain("env EXTRA_KEY=1");
    expect(r.err).toContain("just launchd-install --force com.praesidium.llama-server");
    expect(readFileSync(d, "utf8")).toBe(before);
    expect(r.calls).toEqual([]);
  });

  test("mini's canonical wrkqd is refused and untouched; --force replaces it", () => {
    mkdirSync(agents, { recursive: true });
    const d = dst("com.praesidium.wrkq-server");
    copyFileSync(miniPlist, d);
    const r = just(["launchd-install", "wrkq-server"]);
    expect(r.status).not.toBe(0);
    expect(r.err).toContain("arg --node-tokens-file");
    expect(readFileSync(d, "utf8")).toBe(readFileSync(miniPlist, "utf8"));
    expect(r.calls).toEqual([]);
    const f = just(["launchd-install", "--force", "wrkq-server"]);
    expect(f.status).toBe(0);
    expect(readFileSync(d, "utf8")).toBe(readFileSync(join(repoLaunchd, "com.praesidium.wrkq-server.plist"), "utf8"));
  });

  test("bootstrap retries through error 5 and succeeds", () => {
    const r = just(["launchd-install", "llama-server"], { LAUNCHCTL_BOOTSTRAP_FAILS: "2" });
    expect(r.status).toBe(0);
    expect(r.calls.filter((c) => c.startsWith("launchctl bootstrap")).length).toBe(3);
  });

  test("bootstrap retries about 2s apart by default", () => {
    const t0 = Date.now();
    const r = just(["launchd-install", "llama-server"], { LAUNCHCTL_BOOTSTRAP_FAILS: "1", LAUNCHD_BOOTSTRAP_RETRY_SLEEP: "" });
    expect(r.status).toBe(0);
    expect(Date.now() - t0).toBeGreaterThanOrEqual(2000);
  });

  test("bootstrap that never succeeds fails the recipe after 5 attempts", () => {
    const r = just(["launchd-install"], { LAUNCHCTL_BOOTSTRAP_FAILS: "99" });
    expect(r.status).not.toBe(0);
    expect(r.err).toContain("failed after 5 attempts");
    // The first job failed; the second was never reached.
    expect(r.calls.filter((c) => c.startsWith("launchctl bootstrap")).length).toBe(5);
    expect(r.calls.some((c) => c.includes("wrkq-server"))).toBe(false);
  });

  test("an adopter failure fails the recipe before bootstrap", () => {
    stubAdopter('echo "adopt-agent: boom" >&2; exit 1');
    const r = just(["launchd-install"]);
    expect(r.status).not.toBe(0);
    expect(r.calls.some((c) => c.startsWith("launchctl"))).toBe(false);
  });

  test("unknown job names and sibling recipe names refuse without acting", () => {
    for (const recipe of ["launchd-install", "launchd-uninstall"]) {
      for (const bogus of ["launchd-uninstall", "nope"]) {
        const r = just([recipe, bogus]);
        expect(r.status).not.toBe(0);
        expect(r.err).toContain(`no launchd job ${bogus}`);
        expect(r.calls).toEqual([]);
      }
      const flag = just([recipe, "--forse"]);
      expect(flag.status).not.toBe(0);
      expect(flag.err).toContain("unknown flag --forse");
    }
  });

  test("uninstall boots out and removes each installed copy, adopted or absent", () => {
    stubAdopter();
    expect(just(["launchd-install", "llama-server"]).status).toBe(0);
    writeFileSync(log, "");
    const r = just(["launchd-uninstall"]);
    expect(r.status).toBe(0);
    for (const label of jobs) {
      expect(existsSync(dst(label))).toBe(false);
      expect(r.calls).toContain(`launchctl bootout gui/${uid} ${dst(label)}`);
    }
    expect(r.calls.some((c) => c.includes("bootstrap") || c.includes("kickstart"))).toBe(false);
  });

  test("uninstall refuses to remove a differing installed copy unless --force", () => {
    mkdirSync(agents, { recursive: true });
    const d = dst("com.praesidium.wrkq-server");
    copyFileSync(miniPlist, d);
    const r = just(["launchd-uninstall", "com.praesidium.wrkq-server"]);
    expect(r.status).not.toBe(0);
    expect(r.err).toContain("NOT removing");
    expect(r.err).toContain("just launchd-uninstall --force com.praesidium.wrkq-server");
    expect(existsSync(d)).toBe(true);
    expect(r.calls).toEqual([]);
    const f = just(["launchd-uninstall", "--force", "wrkq-server"]);
    expect(f.status).toBe(0);
    expect(existsSync(d)).toBe(false);
    expect(f.calls).toEqual([`launchctl bootout gui/${uid} ${d}`]);
  });
});
