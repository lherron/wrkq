// Publish @wrkq/client to the local Verdaccio registry.
//
// Trimmed single-package adaptation of agent-spaces/scripts/publish-local-verdaccio.ts.
// @wrkq/client has NO internal workspace dependencies, so the internal-dep pinning
// from the ASP multi-package script is intentionally omitted.
//
// What it enforces (mirrors the ASP guarantees that matter for a consumable dep):
//   - packs with `bun pm pack` (not `npm pack`) from the package's own `files`,
//   - strips `private` and any `bun` export conditions from the PUBLISHED manifest
//     (so consumers resolve the `import` condition → ./dist, never src),
//   - VALIDATES every file referenced by main/types/exports actually exists in the
//     tarball (catches an unbuilt dist before it reaches the registry),
//   - verifies the dist-tag points at the published version after publish.
//
// Usage:
//   bun scripts/publish-local-verdaccio.ts                      # dev: <base>-dev.<ts> tagged latest
//   bun scripts/publish-local-verdaccio.ts --source-versions    # publish package.json version (e.g. 0.1.0) tagged latest
//   bun scripts/publish-local-verdaccio.ts --version 0.1.1 [--tag latest] [--force|--skip-existing]
//   add --dry-run to validate packing without publishing.

import { spawnSync } from 'node:child_process'
import { access, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const ROOT = resolve(import.meta.dir, '..')
const REGISTRY = process.env.VERDACCIO_REGISTRY ?? 'http://127.0.0.1:4873/'

type Manifest = {
  name?: string
  version?: string
  private?: boolean
  main?: string
  types?: string
  exports?: unknown
}

type RegistryMetadata = {
  versions?: Record<string, unknown>
  'dist-tags'?: Record<string, string>
}

type Options = {
  dryRun: boolean
  force: boolean
  skipExisting: boolean
  sourceVersions: boolean
  tag?: string
  version?: string
}

function parseArgs(argv: string[]): Options {
  const options: Options = {
    dryRun: false,
    force: false,
    skipExisting: false,
    sourceVersions: false,
  }
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--dry-run') options.dryRun = true
    else if (arg === '--force') options.force = true
    else if (arg === '--skip-existing') options.skipExisting = true
    else if (arg === '--source-versions') options.sourceVersions = true
    else if (arg === '--version') {
      const v = argv[++i]
      if (!v) throw new Error('--version requires a value')
      options.version = v
    } else if (arg.startsWith('--version=')) options.version = arg.slice('--version='.length)
    else if (arg === '--tag') {
      const v = argv[++i]
      if (!v) throw new Error('--tag requires a value')
      options.tag = v
    } else if (arg.startsWith('--tag=')) options.tag = arg.slice('--tag='.length)
    else if (arg === '--help' || arg === '-h') {
      printHelp()
      process.exit(0)
    } else throw new Error(`Unknown argument: ${arg}`)
  }
  if (options.sourceVersions && options.version) {
    throw new Error('--source-versions cannot be combined with --version')
  }
  if (options.force && options.skipExisting) {
    throw new Error('--force cannot be combined with --skip-existing')
  }
  return options
}

function printHelp(): void {
  console.log(`Usage:
  bun scripts/publish-local-verdaccio.ts [--dry-run]
  bun scripts/publish-local-verdaccio.ts --source-versions [--tag <tag>] [--force|--skip-existing] [--dry-run]
  bun scripts/publish-local-verdaccio.ts --version <semver> [--tag <tag>] [--force|--skip-existing] [--dry-run]

Default mode publishes <base>-dev.YYYYMMDDHHMMSS tagged latest.
--source-versions publishes the version declared in package.json.
--version publishes that exact version. Explicit prereleases require --tag.`)
}

function isSemver(version: string): boolean {
  return /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)
}

function isPrerelease(version: string): boolean {
  return /^\d+\.\d+\.\d+-/.test(version)
}

function timestampVersion(baseVersion: string): string {
  const now = new Date()
  const stamp = [
    now.getFullYear(),
    String(now.getMonth() + 1).padStart(2, '0'),
    String(now.getDate()).padStart(2, '0'),
    String(now.getHours()).padStart(2, '0'),
    String(now.getMinutes()).padStart(2, '0'),
    String(now.getSeconds()).padStart(2, '0'),
  ].join('')
  return `${baseVersion.split('-')[0]}-dev.${stamp}`
}

function resolvePublishVersion(baseVersion: string, options: Options): string {
  if (options.sourceVersions) return baseVersion
  const version = options.version ?? timestampVersion(baseVersion)
  if (!isSemver(version)) throw new Error(`Publish version must be valid semver: ${version}`)
  if (options.version && isPrerelease(version) && !options.tag) {
    throw new Error('Explicit prerelease publishes require --tag')
  }
  return version
}

function run(cmd: string, args: string[], cwd = ROOT): { status: number; out: string } {
  const result = spawnSync(cmd, args, { cwd, encoding: 'utf8' })
  return { status: result.status ?? -1, out: `${result.stdout || ''}${result.stderr || ''}` }
}

function stripBunConditions(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stripBunConditions)
  if (!value || typeof value !== 'object') return value
  const next: Record<string, unknown> = {}
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    if (key === 'bun') continue
    next[key] = stripBunConditions(child)
  }
  return next
}

function findBunConditions(value: unknown, path = 'exports'): string[] {
  if (Array.isArray(value)) {
    return value.flatMap((child, index) => findBunConditions(child, `${path}[${index}]`))
  }
  if (!value || typeof value !== 'object') return []
  const offenders: string[] = []
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    const childPath = `${path}.${key}`
    if (key === 'bun') offenders.push(childPath)
    offenders.push(...findBunConditions(child, childPath))
  }
  return offenders
}

function exportedFilePaths(value: unknown): string[] {
  if (typeof value === 'string' && value.startsWith('./') && !value.includes('*')) return [value]
  if (Array.isArray(value)) return value.flatMap(exportedFilePaths)
  if (!value || typeof value !== 'object') return []
  return Object.values(value as Record<string, unknown>).flatMap(exportedFilePaths)
}

async function assertPackagedFile(packageDir: string, path: string, name: string): Promise<void> {
  const normalized = path.replace(/^\.\//, '')
  try {
    await access(join(packageDir, normalized))
  } catch {
    throw new Error(`${name} tarball references missing file: ${path} (did you run the build?)`)
  }
}

async function registryMetadata(name: string): Promise<RegistryMetadata | undefined> {
  const response = await fetch(`${REGISTRY.replace(/\/$/, '')}/${encodeURIComponent(name)}`)
  if (!response.ok) return undefined
  return (await response.json()) as RegistryMetadata
}

async function versionExists(name: string, version: string): Promise<boolean> {
  const metadata = await registryMetadata(name)
  return Boolean(metadata?.versions?.[version])
}

async function taggedVersion(name: string, tag: string): Promise<string | undefined> {
  const metadata = await registryMetadata(name)
  const version = metadata?.['dist-tags']?.[tag]
  return version && metadata?.versions?.[version] ? version : undefined
}

async function packForPublish(
  publishVersion: string
): Promise<{ name: string; version: string; tarballPath: string; tmp: string }> {
  const packageJsonPath = join(ROOT, 'package.json')
  const originalPackageJson = await readFile(packageJsonPath, 'utf8')
  let tmp = ''
  try {
    tmp = await mkdtemp(join(tmpdir(), 'wrkq-client-publish-'))
    const manifest = JSON.parse(originalPackageJson) as Manifest
    if (!manifest.name || !manifest.version) {
      throw new Error('packages/client/package.json must include name and version')
    }

    const { private: _private, ...manifestWithoutPrivate } = manifest
    const publishManifest = {
      ...manifestWithoutPrivate,
      version: publishVersion,
      exports: stripBunConditions(manifest.exports),
    }
    await writeFile(packageJsonPath, `${JSON.stringify(publishManifest, null, 2)}\n`)

    const pack = run('bun', ['pm', 'pack', '--destination', tmp, '--ignore-scripts'], ROOT)
    if (pack.status !== 0) throw new Error(`bun pm pack failed: ${pack.out}`)

    const entries = await readdir(tmp)
    const tarball = entries.find((entry) => entry.endsWith('.tgz'))
    if (!tarball) throw new Error('bun pm pack produced no tarball')

    const extractDir = join(tmp, 'extract')
    const mkdir = run('mkdir', ['-p', extractDir])
    if (mkdir.status !== 0) throw new Error(`mkdir failed: ${mkdir.out}`)
    const tarballPath = join(tmp, tarball)
    const tar = run('tar', ['-xzf', tarballPath, '-C', extractDir])
    if (tar.status !== 0) throw new Error(`tar failed: ${tar.out}`)

    const extractedPackageDir = join(extractDir, 'package')
    const stagedManifest = JSON.parse(
      await readFile(join(extractedPackageDir, 'package.json'), 'utf8')
    ) as Manifest
    const offenders = findBunConditions(stagedManifest.exports)
    if (offenders.length > 0) {
      throw new Error(`tarball retains bun export conditions: ${offenders.join(', ')}`)
    }
    if (stagedManifest.private) throw new Error('tarball still has private=true')

    const referencedFiles = [
      stagedManifest.main,
      stagedManifest.types,
      ...exportedFilePaths(stagedManifest.exports),
    ].filter((path): path is string => Boolean(path))
    for (const path of new Set(referencedFiles)) {
      await assertPackagedFile(extractedPackageDir, path, manifest.name)
    }

    return { name: manifest.name, version: publishVersion, tarballPath, tmp }
  } catch (error) {
    if (tmp) await rm(tmp, { recursive: true, force: true })
    throw error
  } finally {
    await writeFile(packageJsonPath, originalPackageJson)
  }
}

async function main(): Promise<void> {
  const options = parseArgs(process.argv.slice(2))
  const publishTag = options.tag ?? 'latest'

  const ping = run('npm', ['ping', '--registry', REGISTRY])
  if (ping.status !== 0) throw new Error(`Verdaccio is not reachable at ${REGISTRY}: ${ping.out}`)

  const manifest = (await Bun.file(join(ROOT, 'package.json')).json()) as Manifest
  if (!manifest.version) throw new Error('packages/client/package.json must include version')
  const publishVersion = resolvePublishVersion(manifest.version, options)
  const id = `${manifest.name}@${publishVersion}`

  const packed = await packForPublish(publishVersion)
  try {
    const exists = await versionExists(packed.name, packed.version)
    if (exists && options.skipExisting) {
      console.log(`SKIPPED    ${id} already exists in ${REGISTRY}`)
      return
    }
    if (exists && !options.force && !options.dryRun) {
      throw new Error(`${id} already exists in ${REGISTRY}; use --force to replace it`)
    }
    if (options.dryRun) {
      console.log(`DRY_RUN    ${id} --tag ${publishTag} (packed + validated, not published)`)
      return
    }
    if (options.force && exists) {
      const unpublish = run('npm', ['unpublish', id, '--force', '--registry', REGISTRY])
      if (unpublish.status !== 0 && !/E404|404 Not Found|not found/i.test(unpublish.out)) {
        throw new Error(`npm unpublish failed for ${id}: ${unpublish.out}`)
      }
    }
    const publish = run('npm', [
      'publish',
      packed.tarballPath,
      '--ignore-scripts',
      '--registry',
      REGISTRY,
      '--tag',
      publishTag,
    ])
    if (publish.status !== 0) throw new Error(`npm publish failed for ${id}: ${publish.out}`)

    const tagged = await taggedVersion(packed.name, publishTag)
    if (tagged !== packed.version) {
      throw new Error(`registry ${publishTag} after publishing ${id} is ${tagged ?? '<missing>'}`)
    }
    console.log(`PUBLISHED  ${id} --tag ${publishTag} → ${REGISTRY}`)
  } finally {
    await rm(packed.tmp, { recursive: true, force: true })
  }
}

await main()
