/**
 * publish-local-verdaccio.ts — publish @wrkf/client to a local Verdaccio registry.
 *
 * Modeled on ../agent-spaces/scripts/publish-local-verdaccio.ts but scoped to the
 * single @wrkf/client package, and fully bun-native (all praesidium TS uses bun).
 *
 * The source package.json keeps `private: true`; this script stages a publish
 * manifest (private removed) into a tarball via `bun pm pack`, restores the original
 * manifest, verifies the tarball, then publishes it with `bun publish`. The repo
 * never permanently owns a publishable (private-stripped) manifest on disk.
 *
 * Consumers install via bun against the package's .npmrc (registry → Verdaccio).
 * Note: npm's `min-release-age` cooldown does NOT apply to `bun install`, so no
 * consumer-side workaround is needed.
 *
 * Usage:
 *   bun scripts/publish-local-verdaccio.ts [--tag <tag>] [--tolerate-republish] [--dry-run]
 *
 * Publishes the exact version declared in package.json (0.1.0) tagged `latest`.
 * --tolerate-republish (alias --force) re-publishes an existing same-version
 * without erroring (idempotent local re-runs).
 */

import { spawnSync } from "node:child_process";
import { access, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const ROOT = resolve(import.meta.dir, "..");
const REGISTRY = process.env.VERDACCIO_REGISTRY ?? "http://127.0.0.1:4873/";

type Manifest = {
  name?: string;
  version?: string;
  private?: boolean;
  main?: string;
  types?: string;
  exports?: unknown;
  files?: string[];
};

type Options = { dryRun: boolean; tolerateRepublish: boolean; tag: string };

function parseArgs(argv: string[]): Options {
  const options: Options = { dryRun: false, tolerateRepublish: false, tag: "latest" };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--dry-run") options.dryRun = true;
    else if (arg === "--tolerate-republish" || arg === "--force") options.tolerateRepublish = true;
    else if (arg === "--tag") {
      const value = argv[++i];
      if (!value) throw new Error("--tag requires a value");
      options.tag = value;
    } else if (arg.startsWith("--tag=")) options.tag = arg.slice("--tag=".length);
    else if (arg === "--help" || arg === "-h") {
      console.log(
        "Usage: bun scripts/publish-local-verdaccio.ts [--tag <tag>] [--tolerate-republish] [--dry-run]",
      );
      process.exit(0);
    } else throw new Error(`Unknown argument: ${arg}`);
  }
  return options;
}

function run(cmd: string, args: string[], cwd = ROOT): { status: number; out: string } {
  const result = spawnSync(cmd, args, { cwd, encoding: "utf8" });
  return { status: result.status ?? -1, out: `${result.stdout || ""}${result.stderr || ""}` };
}

function exportedFilePaths(value: unknown): string[] {
  if (typeof value === "string" && value.startsWith("./") && !value.includes("*")) return [value];
  if (Array.isArray(value)) return value.flatMap(exportedFilePaths);
  if (!value || typeof value !== "object") return [];
  return Object.values(value as Record<string, unknown>).flatMap(exportedFilePaths);
}

async function assertPackagedFile(packageDir: string, path: string): Promise<void> {
  const normalized = path.replace(/^\.\//, "");
  try {
    await access(join(packageDir, normalized));
  } catch {
    throw new Error(`tarball references missing file: ${path}`);
  }
}

type RegistryMetadata = { versions?: Record<string, unknown>; "dist-tags"?: Record<string, string> };

async function registryMetadata(name: string): Promise<RegistryMetadata | undefined> {
  const response = await fetch(`${REGISTRY.replace(/\/$/, "")}/${encodeURIComponent(name)}`);
  if (!response.ok) return undefined;
  return (await response.json()) as RegistryMetadata;
}

async function registryReachable(): Promise<boolean> {
  try {
    const response = await fetch(REGISTRY, { method: "GET" });
    return response.ok || response.status === 404;
  } catch {
    return false;
  }
}

async function versionExists(name: string, version: string): Promise<boolean> {
  const metadata = await registryMetadata(name);
  return Boolean(metadata?.versions?.[version]);
}

async function taggedVersion(name: string, tag: string): Promise<string | undefined> {
  const metadata = await registryMetadata(name);
  const version = metadata?.["dist-tags"]?.[tag];
  return version && metadata?.versions?.[version] ? version : undefined;
}

async function packForPublish(): Promise<{ name: string; version: string; tarballPath: string; tmp: string }> {
  const packageJsonPath = join(ROOT, "package.json");
  const originalPackageJson = await readFile(packageJsonPath, "utf8");
  let tmp = "";
  try {
    tmp = await mkdtemp(join(tmpdir(), "wrkf-client-publish-"));
    const manifest = JSON.parse(originalPackageJson) as Manifest;
    if (!manifest.name || !manifest.version) {
      throw new Error("package.json must include name and version");
    }

    // Stage a publish manifest: drop `private`, and ship only the TS source via `files`.
    const { private: _private, ...rest } = manifest;
    const publishManifest = { ...rest, files: ["src"] };
    await writeFile(packageJsonPath, `${JSON.stringify(publishManifest, null, 2)}\n`);

    const pack = run("bun", ["pm", "pack", "--destination", tmp, "--ignore-scripts"], ROOT);
    if (pack.status !== 0) throw new Error(`bun pm pack failed: ${pack.out}`);

    const entries = await readdir(tmp);
    const tarball = entries.find((entry) => entry.endsWith(".tgz"));
    if (!tarball) throw new Error("bun pm pack produced no tarball");

    const extractDir = join(tmp, "extract");
    if (run("mkdir", ["-p", extractDir]).status !== 0) throw new Error("mkdir extract failed");
    const tarballPath = join(tmp, tarball);
    const tar = run("tar", ["-xzf", tarballPath, "-C", extractDir]);
    if (tar.status !== 0) throw new Error(`tar failed: ${tar.out}`);

    const extractedPackageDir = join(extractDir, "package");
    const stagedManifest = JSON.parse(
      await readFile(join(extractedPackageDir, "package.json"), "utf8"),
    ) as Manifest;
    if (stagedManifest.private) throw new Error("tarball still has private=true");

    const referencedFiles = [
      stagedManifest.main,
      stagedManifest.types,
      ...exportedFilePaths(stagedManifest.exports),
      "./src/index.ts",
      "./src/client.ts",
      "./src/types.ts",
    ].filter((path): path is string => Boolean(path));
    for (const path of new Set(referencedFiles)) {
      await assertPackagedFile(extractedPackageDir, path);
    }

    return { name: manifest.name, version: manifest.version, tarballPath, tmp };
  } catch (error) {
    if (tmp) await rm(tmp, { recursive: true, force: true });
    throw error;
  } finally {
    // Always restore the original (private:true) source manifest.
    await writeFile(packageJsonPath, originalPackageJson);
  }
}

async function main() {
  const options = parseArgs(process.argv.slice(2));

  if (!(await registryReachable())) {
    throw new Error(`Verdaccio is not reachable at ${REGISTRY}`);
  }

  const packed = await packForPublish();
  const id = `${packed.name}@${packed.version}`;

  try {
    const exists = await versionExists(packed.name, packed.version);

    if (options.dryRun) {
      console.log(
        `DRY_RUN  ${id} --tag ${options.tag} exists=${exists} (tarball: ${packed.tarballPath})`,
      );
      return;
    }

    if (exists && !options.tolerateRepublish) {
      throw new Error(`${id} already exists in ${REGISTRY}; use --tolerate-republish to re-publish`);
    }

    const publishArgs = [
      "publish",
      packed.tarballPath,
      "--registry",
      REGISTRY,
      "--tag",
      options.tag,
      "--ignore-scripts",
    ];
    if (options.tolerateRepublish) publishArgs.push("--tolerate-republish");

    const publish = run("bun", publishArgs);
    if (publish.status !== 0) throw new Error(`bun publish failed for ${id}: ${publish.out}`);

    const tagged = await taggedVersion(packed.name, options.tag);
    if (tagged !== packed.version) {
      throw new Error(`registry ${options.tag} after publishing ${id} is ${tagged ?? "<missing>"}`);
    }

    console.log(`PUBLISHED  ${id} --tag ${options.tag} to ${REGISTRY}`);
  } finally {
    await rm(packed.tmp, { recursive: true, force: true });
  }
}

await main();
