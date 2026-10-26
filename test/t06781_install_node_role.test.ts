import { describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import {
  chmodSync,
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

// The install body runs for real, but hermetically: cwd is a scratch dir holding
// only scripts/resolve-node-role.sh, HOME is a scratch home, and every command
// that could reach the host (just, cp, git, uname, launchctl, codesign, go, bun,
// wrkq, wrkqadm) is a PATH stub. uname reports a non-Darwin host, so the launchd
// restart section (covered by t08927/t10825) is skipped; reaching launchctl or
// codesign anyway is logged and fails the run.
const repoRoot = process.cwd();
const resolverPath = join(repoRoot, "scripts", "resolve-node-role.sh");
const registryBaseline = '{"versions":["0.1.0-dev.canonical"],"latest":"0.1.0-dev.canonical"}\n';

type FixtureOptions = {
  defaultRole?: string;
  envRole?: string;
  hostIdentity?: string;
};

type Fixture = {
  callsPath: string;
  cleanup: () => void;
  cwd: string;
  env: Record<string, string>;
  registryPath: string;
};

function writeExecutable(path: string, contents: string): void {
  writeFileSync(path, contents);
  chmodSync(path, 0o755);
}

function createFixture(options: FixtureOptions = {}): Fixture {
  const root = mkdtempSync(join(tmpdir(), "wrkq-install-role-"));
  const home = join(root, "home");
  const stubs = join(root, "stubs");
  const callsPath = join(root, "just-calls");
  const registryPath = join(root, "registry-state.json");
  const cwd = join(root, "checkout");
  mkdirSync(home, { recursive: true });
  mkdirSync(stubs, { recursive: true });
  mkdirSync(join(cwd, "scripts"), { recursive: true });
  copyFileSync(resolverPath, join(cwd, "scripts", "resolve-node-role.sh"));
  writeFileSync(callsPath, "");
  writeFileSync(registryPath, registryBaseline);

  if (options.defaultRole !== undefined) {
    const configDir = join(home, ".config", "wrkq");
    mkdirSync(configDir, { recursive: true });
    writeFileSync(join(configDir, "node-role"), `${options.defaultRole}\n`);
  }

  writeExecutable(
    join(stubs, "just"),
    `#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "$*" >> "$WRKQ_TEST_CALLS"
if [ "$1" = "client-publish-dev" ]; then
  printf '%s\\n' '{"versions":["0.1.0-dev.canonical","0.1.0-dev.minted"],"latest":"0.1.0-dev.minted"}' > "$WRKQ_TEST_REGISTRY"
fi
`,
  );
  writeExecutable(
    join(stubs, "cp"),
    `#!/usr/bin/env bash
set -euo pipefail
destination=""
for argument in "$@"; do
  destination="$argument"
done
mkdir -p "$(dirname "$destination")"
: > "$destination"
`,
  );
  writeExecutable(join(stubs, "hostname"), `#!/usr/bin/env bash\nprintf '%s\\n' '${options.hostIdentity ?? "fixture-host"}'\n`);
  writeExecutable(join(stubs, "uname"), `#!/usr/bin/env bash\nprintf '%s\\n' '${options.hostIdentity ?? "fixture-host"}'\n`);
  // Not a work tree: the dirty-tree guard is out of scope here.
  writeExecutable(join(stubs, "git"), "#!/usr/bin/env bash\nexit 128\n");
  for (const hostCommand of ["launchctl", "codesign", "go", "bun", "wrkq", "wrkqadm"]) {
    writeExecutable(
      join(stubs, hostCommand),
      `#!/usr/bin/env bash\nprintf '%s\\n' "UNEXPECTED ${hostCommand} $*" >> "$WRKQ_TEST_CALLS"\nexit 97\n`,
    );
  }

  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      (entry): entry is [string, string] =>
        entry[1] !== undefined &&
        entry[0] !== "WRKQ_NODE_ROLE" &&
        entry[0] !== "WRKQ_NODE_ROLE_FILE" &&
        !entry[0].startsWith("GIT_"),
    ),
  );
  env.HOME = home;
  env.PATH = `${stubs}:${process.env.PATH ?? "/usr/bin:/bin"}`;
  env.WRKQ_TEST_CALLS = callsPath;
  env.WRKQ_TEST_REGISTRY = registryPath;
  if (options.envRole !== undefined) {
    env.WRKQ_NODE_ROLE = options.envRole;
  }
  if (options.hostIdentity !== undefined) {
    env.HOST = options.hostIdentity;
    env.HOSTNAME = options.hostIdentity;
    env.WRKQ_NODE_ID = options.hostIdentity;
  }

  return {
    callsPath,
    cleanup: () => rmSync(root, { force: true, recursive: true }),
    cwd,
    env,
    registryPath,
  };
}

/** Renders `just install {flags}`; any other recipe variable fails loudly rather than rendering as text. */
function renderInstallBody(flags: string): string {
  const dump = JSON.parse(
    execFileSync("just", ["--dump", "--dump-format", "json"], {
      cwd: repoRoot,
      encoding: "utf8",
    }),
  );
  const render = (fragment: unknown): string => {
    if (typeof fragment === "string") return fragment;
    if (!Array.isArray(fragment)) return "";
    if (fragment[0] === "variable") {
      if (fragment[1] === "flags") return flags;
      throw new Error(`install body uses an unrendered recipe variable: ${String(fragment[1])}`);
    }
    return fragment.map(render).join("");
  };
  return dump.recipes.install.body.map(render).join("\n");
}

function runInstall(fixture: Fixture, flags = "") {
  const result = Bun.spawnSync({
    cmd: ["bash", "-c", renderInstallBody(flags)],
    cwd: fixture.cwd,
    env: fixture.env,
    stdout: "pipe",
    stderr: "pipe",
  });
  const calls = readFileSync(fixture.callsPath, "utf8").trim().split("\n").filter(Boolean);
  return {
    calls,
    output: `${result.stdout.toString()}${result.stderr.toString()}`,
    registry: readFileSync(fixture.registryPath, "utf8"),
    status: result.exitCode,
  };
}

function resolveRole(fixture: Fixture) {
  const result = Bun.spawnSync({
    cmd: ["bash", resolverPath],
    cwd: fixture.cwd,
    env: fixture.env,
    stdout: "pipe",
    stderr: "pipe",
  });
  return {
    output: `${result.stdout.toString()}${result.stderr.toString()}`,
    role: result.stdout.toString().trim(),
    status: result.exitCode,
  };
}

describe("T-06781 install node-role gate", () => {
  test("consumer, missing, and invalid roles skip publish and preserve registry state", () => {
    for (const options of [
      { defaultRole: "producer", envRole: "consumer" },
      {},
      { defaultRole: "mini" },
      // Host identity is ignored: HOST/HOSTNAME/WRKQ_NODE_ID=svc without a marker is the missing-role default.
      { hostIdentity: "svc" },
    ]) {
      const fixture = createFixture(options);
      try {
        const install = runInstall(fixture);
        expect(install.status, install.output).toBe(0);
        expect(install.calls).toEqual(["build"]);
        expect(install.registry).toBe(registryBaseline);
        expect(install.output).toContain("[install] consumer node role; skipping client publish and downstream sync");

        const resolution = resolveRole(fixture);
        expect(resolution.status, resolution.output).toBe(0);
        expect(resolution.role).toBe("consumer");
      } finally {
        fixture.cleanup();
      }
    }
  });

  test("producer env and persistent file roles publish while retaining no-sync semantics", () => {
    const fileFixture = createFixture({ defaultRole: "producer" });
    try {
      const install = runInstall(fileFixture);
      expect(install.status, install.output).toBe(0);
      expect(install.calls).toEqual(["build", "client-publish-dev", "sync-downstream"]);
      expect(install.registry).not.toBe(registryBaseline);

      const resolution = resolveRole(fileFixture);
      expect(resolution.status, resolution.output).toBe(0);
      expect(resolution.role).toBe("producer");
    } finally {
      fileFixture.cleanup();
    }

    const envFixture = createFixture({ envRole: "producer" });
    try {
      const install = runInstall(envFixture, "no-sync=1");
      expect(install.status, install.output).toBe(0);
      expect(install.calls).toEqual(["build", "client-publish-dev"]);
      expect(install.output).toContain("[install] skipping downstream sync (no-sync=1)");

      const resolution = resolveRole(envFixture);
      expect(resolution.status, resolution.output).toBe(0);
      expect(resolution.role).toBe("producer");
    } finally {
      envFixture.cleanup();
    }
  });
});
