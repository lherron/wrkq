#!/usr/bin/env node
import { spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join, relative, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { performance } from 'node:perf_hooks'

const GUARD_ID = 'fit:s6/hook-runs-verify'
const OWNER_PROJECT = 'wrkq'
const ENTRYPOINT = 'tools/fitkit/s6-hook-runs-verify.mjs'
const PROVENANCE_FILE = 'tools/fitkit/s6-hook-runs-verify.provenance.json'
const PRE_PUSH_HOOK = '.git/hooks/pre-push'
const DEFAULT_BUDGET_MS = 100
const PROVENANCE_IDENTIFIER = `${GUARD_ID}@wrkq-local`

function diagnostic(code, fix, why, detail) {
  return { code, fix, why, detail }
}

function fail(diag, json = false, output) {
  if (json && output) {
    console.log(JSON.stringify({ ...output, ok: false, diagnostic: diag }, null, 2))
  } else {
    const detail = diag.detail ? `\nDETAIL: ${JSON.stringify(diag.detail)}` : ''
    console.error([`CODE: ${diag.code}`, `FIX: ${diag.fix}`, `WHY: ${diag.why}${detail}`].join('\n'))
  }
  process.exit(1)
}

function parseArgs(argv = process.argv.slice(2)) {
  const args = { root: process.cwd(), budgetMs: DEFAULT_BUDGET_MS, json: false }

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index]
    if (arg === '--root') {
      const value = argv[index + 1]
      if (!value || value.startsWith('--')) {
        fail(diagnostic('cli.root.missing', 'Pass a path after --root.', '--root requires the repository root to evaluate.'))
      }
      args.root = value
      index += 1
    } else if (arg === '--gate-budget-ms') {
      args.budgetMs = Number(argv[index + 1] ?? NaN)
      index += 1
    } else if (arg === '--json') {
      args.json = true
    } else if (arg === '--help' || arg === '-h') {
      console.log('Usage: node tools/fitkit/s6-hook-runs-verify.mjs [--root <repo>] [--gate-budget-ms <ms>] [--json]')
      process.exit(0)
    } else {
      fail(diagnostic(
        'cli.unknown-argument',
        `Remove unknown argument ${arg}.`,
        'The S6 hook guard accepts only --root, --gate-budget-ms, --json, and --help.',
        { arg },
      ))
    }
  }

  if (!Number.isFinite(args.budgetMs) || args.budgetMs <= 0) {
    fail(diagnostic(
      'cli.budget.invalid',
      'Pass a positive numeric --gate-budget-ms value.',
      'The gate budget must be a finite positive number.',
      { budgetMs: args.budgetMs },
    ))
  }

  return { ...args, root: resolve(args.root) }
}

function readText(root, path) {
  const absolute = join(root, path)
  return existsSync(absolute) ? readFileSync(absolute, 'utf8') : undefined
}

function resolveGitPath(root, path) {
  const result = spawnSync('git', ['-C', root, 'rev-parse', '--git-path', path], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  const gitPath = (result.stdout || '').trim()
  if (result.status !== 0 || gitPath.length === 0) {
    return {
      ok: false,
      diagnostic: diagnostic(
        'hook.path.unresolvable',
        `Run the guard from a Git repository with a resolvable ${path} path.`,
        `${GUARD_ID} asks Git where hooks are stored so linked worktrees and configured hook paths are evaluated correctly.`,
        {
          root,
          gitPath: path,
          error: result.error?.message || (result.stderr || '').trim() || `git exited ${result.status}`,
        },
      ),
    }
  }

  return {
    ok: true,
    path: isAbsolute(gitPath) ? resolve(gitPath) : resolve(root, gitPath),
  }
}

function resolvePrePushHook(root) {
  const hooks = resolveGitPath(root, 'hooks')
  if (!hooks.ok) return hooks
  return { ok: true, path: join(hooks.path, 'pre-push') }
}

function findJustfile(root) {
  if (existsSync(join(root, 'Justfile'))) return 'Justfile'
  if (existsSync(join(root, 'justfile'))) return 'justfile'
  return undefined
}

function hasVerifyTarget(content) {
  return content.split(/\r?\n/).some((line) => /^verify(?:\s+[^:#]+)?\s*:/.test(line))
}

function hookContainsJustVerify(content) {
  return content.split(/\r?\n/).some((line) => {
    const trimmed = line.trim()
    return trimmed.length > 0 && !trimmed.startsWith('#') && /^just\s+verify(?:\s|$)/.test(trimmed)
  })
}

function evaluateGuard(root) {
  const justfile = findJustfile(root)
  if (!justfile) {
    return {
      passed: false,
      result: { level: 'ABSENT', exercise: 'DORMANT' },
      detail: { verifyTargetPresent: false, prePushHookPresent: false, prePushRunsVerify: false },
      diagnostic: diagnostic(
        'justfile.missing',
        'Create Justfile or justfile with a verify target.',
        `${GUARD_ID} requires an inspectable just verify target.`,
        { checked: ['Justfile', 'justfile'] },
      ),
    }
  }

  const justfileContent = readText(root, justfile)
  const verifyTargetPresent = justfileContent !== undefined && hasVerifyTarget(justfileContent)
  const hook = resolvePrePushHook(root)
  if (!hook.ok) {
    return {
      passed: false,
      result: { level: 'ABSENT', exercise: 'DORMANT' },
      detail: { justfile, verifyTargetPresent, prePushHookPresent: false, prePushRunsVerify: false },
      diagnostic: hook.diagnostic,
    }
  }
  const hookContent = existsSync(hook.path) ? readFileSync(hook.path, 'utf8') : undefined
  const prePushHookPresent = hookContent !== undefined
  const prePushRunsVerify = hookContent !== undefined && hookContainsJustVerify(hookContent)
  const passed = verifyTargetPresent && prePushHookPresent && prePushRunsVerify
  const detail = {
    justfile,
    verifyTargetPresent,
    prePushHookPresent,
    prePushRunsVerify,
    resolvedPrePushHook: hook.path,
  }

  if (passed) {
    return { passed, result: { level: 'PRESENT', exercise: 'EXERCISED' }, detail }
  }
  if (!verifyTargetPresent) {
    return {
      passed,
      result: { level: 'ABSENT', exercise: 'DORMANT' },
      detail,
      diagnostic: diagnostic(
        'justfile.verify-target.missing',
        'Add a verify target to Justfile or justfile.',
        `${GUARD_ID} requires the repository verify command to exist.`,
        { justfile },
      ),
    }
  }
  if (!prePushHookPresent) {
    return {
      passed,
      result: { level: 'ABSENT', exercise: 'DORMANT' },
      detail,
      diagnostic: diagnostic(
        'hook.pre-push.missing',
        `Install ${PRE_PUSH_HOOK}.`,
        `${GUARD_ID} verifies the installed git pre-push hook, not a template.`,
        { hook: PRE_PUSH_HOOK },
      ),
    }
  }
  return {
    passed,
    result: { level: 'ABSENT', exercise: 'DORMANT' },
    detail,
    diagnostic: diagnostic(
      'hook.pre-push.verify.missing',
      `Add a non-comment line matching "just verify" to ${PRE_PUSH_HOOK}.`,
      `${GUARD_ID} requires the installed pre-push hook to delegate to just verify.`,
      { hook: PRE_PUSH_HOOK },
    ),
  }
}

function parseProvenance(root) {
  const content = readText(root, PROVENANCE_FILE)
  if (content === undefined) {
    return {
      ok: false,
      diagnostic: diagnostic(
        'provenance.file.missing',
        `Create ${PROVENANCE_FILE}.`,
        'The local S6 guard requires committed first-party provenance.',
        { provenance: PROVENANCE_FILE },
      ),
    }
  }

  let manifest
  try {
    manifest = JSON.parse(content)
  } catch (error) {
    return {
      ok: false,
      diagnostic: diagnostic(
        'provenance.file.malformed',
        `Fix JSON syntax in ${PROVENANCE_FILE}.`,
        'The local S6 guard provenance manifest must be parseable JSON.',
        { provenance: PROVENANCE_FILE, error: error instanceof Error ? error.message : String(error) },
      ),
    }
  }

  const localSourceFiles = Array.isArray(manifest.localSourceFiles) ? manifest.localSourceFiles : []
  const checkedSurfaces = Array.isArray(manifest.checkedSurfaces) ? manifest.checkedSurfaces : []
  const failures = []
  if (manifest.guardId !== GUARD_ID) failures.push(`guardId must be ${GUARD_ID}`)
  if (manifest.ownerProject !== OWNER_PROJECT) failures.push(`ownerProject must be ${OWNER_PROJECT}`)
  if (!localSourceFiles.includes(ENTRYPOINT)) failures.push(`localSourceFiles must include ${ENTRYPOINT}`)
  if (!checkedSurfaces.some((surface) => surface === 'Justfile' || surface === 'justfile')) failures.push('checkedSurfaces must include Justfile or justfile')
  if (!checkedSurfaces.includes(PRE_PUSH_HOOK)) failures.push(`checkedSurfaces must include ${PRE_PUSH_HOOK}`)

  if (failures.length > 0) {
    return {
      ok: false,
      diagnostic: diagnostic(
        'provenance.file.invalid',
        `Update ${PROVENANCE_FILE} to identify this local wrkq guard and its checked surfaces.`,
        'The local S6 guard provenance manifest is incomplete or points at the wrong guard.',
        { provenance: PROVENANCE_FILE, failures },
      ),
    }
  }

  return { ok: true, manifest, identifier: PROVENANCE_IDENTIFIER }
}

function copyIfPresent(sourceRoot, targetRoot, path) {
  const content = readText(sourceRoot, path)
  if (content === undefined) return false
  const target = join(targetRoot, path)
  mkdirSync(dirname(target), { recursive: true })
  writeFileSync(target, content)
  return true
}

function perturbHook(hookPath) {
  if (!existsSync(hookPath)) return false
  const original = readFileSync(hookPath, 'utf8')
  const perturbed = original.replace(/^(\s*)just\s+verify(?:\s.*)?$/gm, '$1# fitkit negative smoke removed just verify')
  writeFileSync(hookPath, perturbed)
  return perturbed !== original
}

function runNegativeSmoke(root) {
  const smokeRoot = mkdtempSync(join(tmpdir(), 'fitkit-s6-smoke-'))
  try {
    const sourceHook = resolvePrePushHook(root)
    if (!sourceHook.ok) return sourceHook

    const gitInit = spawnSync('git', ['init', '--quiet', smokeRoot], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    if (gitInit.status !== 0) {
      return {
        ok: false,
        diagnostic: diagnostic(
          'smoke.git-init.failed',
          'Ensure git can initialize the isolated negative-smoke repository.',
          'The negative smoke needs a genuine Git hooks path to exercise the same lookup as the target repository.',
          { root: smokeRoot, error: gitInit.error?.message || (gitInit.stderr || '').trim() || `git exited ${gitInit.status}` },
        ),
      }
    }
    const targetHook = resolvePrePushHook(smokeRoot)
    if (!targetHook.ok) return targetHook

    const justfile = findJustfile(root)
    if (justfile) copyIfPresent(root, smokeRoot, justfile)
    if (existsSync(sourceHook.path)) {
      mkdirSync(dirname(targetHook.path), { recursive: true })
      writeFileSync(targetHook.path, readFileSync(sourceHook.path, 'utf8'))
    }
    copyIfPresent(root, smokeRoot, PROVENANCE_FILE)
    copyIfPresent(root, smokeRoot, ENTRYPOINT)

    const mutated = perturbHook(targetHook.path)
    if (!mutated) {
      return {
        ok: false,
        diagnostic: diagnostic(
          'smoke.perturbation.noop',
          `Ensure ${PRE_PUSH_HOOK} contains a removable non-comment "just verify" line before smoke runs.`,
          'The negative smoke must actually perturb a copied installed hook.',
          { hook: PRE_PUSH_HOOK },
        ),
      }
    }

    const provenance = parseProvenance(smokeRoot)
    if (!provenance.ok) {
      return {
        ok: false,
        diagnostic: diagnostic(
          'smoke.provenance.failed',
          `Keep ${PROVENANCE_FILE} copyable into the negative-smoke root.`,
          'The negative smoke could not evaluate the same local provenance contract.',
          { cause: provenance.diagnostic },
        ),
      }
    }

    const guard = evaluateGuard(smokeRoot)
    const observed = guard.passed ? 'PASS' : 'FAIL'
    if (observed !== 'FAIL') {
      return {
        ok: false,
        diagnostic: diagnostic(
          'smoke.negative.mismatch',
          'Fix the guard so removing the installed hook delegation fails.',
          'The negative smoke must observe a real guard failure after perturbing the temp root.',
          { expected: 'FAIL', observed, detail: guard.detail },
        ),
      }
    }
    return { ok: true, smoke: { perturbation: 'remove-hook-verify-line', expected: 'FAIL', observed, detail: guard.detail } }
  } finally {
    rmSync(smokeRoot, { recursive: true, force: true })
  }
}

function assertEntrypointLocation(root) {
  const expectedPath = join(root, ENTRYPOINT)
  try {
    const expected = realpathSync(expectedPath)
    const actual = realpathSync(fileURLToPath(import.meta.url))
    if (expected !== actual) {
      return {
        ok: false,
        diagnostic: diagnostic(
          'entrypoint.location.invalid',
          `Run ${ENTRYPOINT} from the target repository root.`,
          'The S6 guard validates the committed local entrypoint named by provenance.',
          { expected: relative(process.cwd(), expectedPath), actual: fileURLToPath(import.meta.url) },
        ),
      }
    }
  } catch (error) {
    return {
      ok: false,
      diagnostic: diagnostic(
        'entrypoint.location.unresolvable',
        `Ensure ${ENTRYPOINT} exists under the target repository root.`,
        'The S6 guard could not resolve its committed local entrypoint.',
        { expected: relative(process.cwd(), expectedPath), error: error instanceof Error ? error.message : String(error) },
      ),
    }
  }
  return { ok: true }
}

function outputPayload({ guard, smoke, provenance, durationMs, budgetMs }) {
  return {
    id: GUARD_ID,
    result: guard.result,
    smoke: smoke.smoke,
    provenanceIdentifier: provenance.identifier,
    provenance: {
      file: PROVENANCE_FILE,
      guardId: provenance.manifest.guardId,
      ownerProject: provenance.manifest.ownerProject,
      localSourceFiles: provenance.manifest.localSourceFiles,
      checkedSurfaces: provenance.manifest.checkedSurfaces,
    },
    durationMs: Number(durationMs.toFixed(1)),
    budgetMs,
    detail: guard.detail,
  }
}

async function main() {
  const args = parseArgs()
  const started = performance.now()
  const baseOutput = { id: GUARD_ID, provenanceIdentifier: PROVENANCE_IDENTIFIER }

  const location = assertEntrypointLocation(args.root)
  if (!location.ok) fail(location.diagnostic, args.json, baseOutput)

  const provenance = parseProvenance(args.root)
  if (!provenance.ok) fail(provenance.diagnostic, args.json, baseOutput)

  const guard = evaluateGuard(args.root)
  if (!guard.passed) fail(guard.diagnostic, args.json, {
    ...baseOutput,
    result: guard.result,
    provenanceIdentifier: provenance.identifier,
    detail: guard.detail,
  })

  const smoke = runNegativeSmoke(args.root)
  if (!smoke.ok) fail(smoke.diagnostic, args.json, {
    ...baseOutput,
    result: guard.result,
    provenanceIdentifier: provenance.identifier,
    detail: guard.detail,
  })

  const durationMs = performance.now() - started
  const payload = outputPayload({ guard, smoke, provenance, durationMs, budgetMs: args.budgetMs })

  if (args.json) {
    console.log(JSON.stringify(payload, null, 2))
  } else {
    console.log(`${GUARD_ID} ok: installed pre-push hook delegates to just verify; negative smoke observed FAIL; provenance ${provenance.identifier} (${payload.durationMs}ms, budget ${args.budgetMs}ms)`)
  }
}

function isEntrypoint() {
  if (!process.argv[1] || process.argv[1] === '-' || process.argv[1].startsWith('[')) return false
  try {
    return realpathSync(fileURLToPath(import.meta.url)) === realpathSync(process.argv[1])
  } catch {
    return true
  }
}

if (isEntrypoint()) {
  await main()
}
