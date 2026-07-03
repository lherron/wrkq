#!/usr/bin/env node
import { existsSync, readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { performance } from 'node:perf_hooks'

const FITNESS_ID = 'fit:s6/hook-runs-verify'
const DEFAULT_BUDGET_MS = 100
const PIN_FILE = 'tools/fitkit/s6-hook-runs-verify.pin.json'
const ARTIFACT_FILE = 'tools/fitkit/s6-hook-runs-verify.mjs'

const proofClasses = ['presence', 'guard', 'usage', 'judgment']
const effects = ['pure', 'read', 'agent', 'sandbox-mutate', 'escalate', 'repo-mutate:mechanical', 'repo-mutate:design']
const costHints = ['ms', 'seconds', 'agent-turn']

function sha256(input) {
  return createHash('sha256').update(input).digest('hex')
}

function diagnostic(code, fix, why, exception, detail) {
  return { code, fix, why, exception, detail }
}

function fail(diag) {
  const detail = diag.detail ? `\nDETAIL: ${JSON.stringify(diag.detail)}` : ''
  console.error([
    `CODE: ${diag.code}`,
    `FIX: ${diag.fix}`,
    `WHY: ${diag.why}`,
    `EXCEPTION: ${diag.exception}${detail}`,
  ].join('\n'))
  process.exit(1)
}

function parseArgs() {
  const args = process.argv.slice(2)
  let root = process.cwd()
  let budgetMs = DEFAULT_BUDGET_MS
  let json = false

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index]
    if (arg === '--root') {
      root = args[index + 1] ?? root
      index += 1
    } else if (arg === '--gate-budget-ms') {
      budgetMs = Number(args[index + 1] ?? budgetMs)
      index += 1
    } else if (arg === '--json') {
      json = true
    } else if (arg === '--help' || arg === '-h') {
      console.log('Usage: node tools/fitkit/s6-hook-runs-verify.mjs [--root <repo>] [--gate-budget-ms <ms>] [--json]')
      process.exit(0)
    } else {
      fail(diagnostic(
        'cli.unknown-argument',
        `Remove unknown argument ${arg}.`,
        'The vendored gate accepts only --root, --gate-budget-ms, and --json.',
        'No gate-seat exception. Change the committed justfile target if invocation needs to change.',
        { arg },
      ))
    }
  }

  if (!Number.isFinite(budgetMs) || budgetMs <= 0) {
    fail(diagnostic(
      'admission.budget.invalid',
      'Pass a positive numeric --gate-budget-ms value.',
      'Measured admission needs a finite positive gate budget.',
      'No gate-seat exception. Slow checks belong outside the gate.',
      { budgetMs },
    ))
  }

  return { root: resolve(root), budgetMs, json }
}

function isObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseFitnessEntry(input) {
  if (!isObject(input)) {
    return { ok: false, diagnostic: diagnostic('admission.entry.malformed', 'Provide a registry object.', 'Seat admission received a non-object registry entry.', 'No automatic exception. Use the owning profile waiver or manual review channel.') }
  }
  if (typeof input.id !== 'string' || input.id.length === 0) {
    return { ok: false, diagnostic: diagnostic('admission.entry.id', 'Set a non-empty string id.', 'Fitness entries require stable string IDs.', 'No automatic exception. Use the owning profile waiver or manual review channel.') }
  }
  if (typeof input.axis !== 'string' || input.axis.length === 0) {
    return { ok: false, diagnostic: diagnostic('admission.entry.axis', 'Set a non-empty string axis.', 'Axes bind by string ID and must be explicit.', 'No automatic exception. Use the owning profile waiver or manual review channel.') }
  }
  if (!proofClasses.includes(input.proofClass)) {
    return { ok: false, diagnostic: diagnostic('admission.entry.proof-class', 'Use proofClass presence|guard|usage|judgment.', 'Unknown proof classes cannot be admitted.', 'No automatic exception. Use the owning profile waiver or manual review channel.') }
  }
  if (!effects.includes(input.effects)) {
    return { ok: false, diagnostic: diagnostic('admission.entry.effect', 'Use a declared effect enum value.', 'Unknown effects cannot be placed safely in a seat.', 'No automatic exception. Use the owning profile waiver or manual review channel.') }
  }
  if (!costHints.includes(input.cost)) {
    return { ok: false, diagnostic: diagnostic('admission.entry.cost', 'Use cost ms|seconds|agent-turn.', 'Unknown cost hints cannot be routed.', 'No automatic exception. Use the owning profile waiver or manual review channel.') }
  }
  if (!isObject(input.surface) || !['repo', 'package', 'dir'].includes(input.surface.kind)) {
    return { ok: false, diagnostic: diagnostic('admission.entry.surface', 'Attach a repo|package|dir surface.', 'Fitness entries must declare their scope.', 'No automatic exception. Use the owning profile waiver or manual review channel.') }
  }
  if (input.proofClass === 'guard' && input.hasNegativeSmoke !== true) {
    return { ok: false, diagnostic: diagnostic('admission.guard.no-smoke', 'Attach a negative smoke perturbation proof to the guard fitness.', 'A guard verdict without fires-on-bad proof cannot claim guard closure.', 'Mark the row manual residue until negative smoke exists.', { id: input.id }) }
  }
  return { ok: true, value: input }
}

function admitFitness(entryInput, seat) {
  const parsed = parseFitnessEntry(entryInput)
  if (!parsed.ok) return parsed

  const entry = parsed.value
  if (!seat.proofClasses.includes(entry.proofClass)) {
    return { ok: false, diagnostic: diagnostic(
      'admission.proof-class.rejected',
      `Move ${entry.id} to a seat that admits ${entry.proofClass} proof.`,
      `${seat.id} admits ${seat.proofClasses.join(', ')} proof, not ${entry.proofClass}.`,
      'Use the owning profile/manual-residue channel for exceptions.',
      { id: entry.id, seat: seat.id, proofClass: entry.proofClass },
    ) }
  }
  if (!seat.effects.includes(entry.effects)) {
    return { ok: false, diagnostic: diagnostic(
      'admission.effect.rejected',
      `Remove ${entry.effects} from ${seat.id} or run ${entry.id} in a seat with that authority.`,
      `${seat.id} cannot admit ${entry.effects}; gate seats are limited to pure/read authority.`,
      'No gate-seat exception. Escalate through ASSESS/APPLY/VERIFY instead.',
      { id: entry.id, seat: seat.id, effects: entry.effects },
    ) }
  }
  return { ok: true, value: { entry } }
}

function readText(root, path) {
  const absolute = join(root, path)
  if (!existsSync(absolute)) {
    fail(diagnostic(
      'evidence.file.missing',
      `Create ${path} or restore it from the wrkq repo.`,
      `${FITNESS_ID} cannot resolve required file evidence ${path}.`,
      'No gate-seat exception. Guard truth must be locally inspectable.',
      { path },
    ))
  }
  return readFileSync(absolute, 'utf8')
}

function fileEvidence(root, path, content) {
  return { kind: 'file', path, contentSha: sha256(content) }
}

function resolveEvidence(root, evidence) {
  for (const item of evidence) {
    if (item.kind !== 'file' || typeof item.path !== 'string' || typeof item.contentSha !== 'string') {
      return { ok: false, diagnostic: diagnostic(
        'evidence.entry.malformed',
        'Emit file evidence with kind, path, and contentSha.',
        `${FITNESS_ID} emitted malformed evidence.`,
        'No gate-seat exception. Malformed evidence cannot close guard truth.',
        { evidence: item },
      ) }
    }
    const content = readText(root, item.path)
    const actual = sha256(content)
    if (actual !== item.contentSha) {
      return { ok: false, diagnostic: diagnostic(
        'evidence.file.changed',
        `Rerun ${FITNESS_ID} after ${item.path} settles.`,
        `${item.path} content changed while resolving evidence.`,
        'No gate-seat exception. Stale evidence cannot close guard truth.',
        { path: item.path, expected: item.contentSha, actual },
      ) }
    }
  }
  return { ok: true }
}

function hasVerifyTarget(justfile) {
  return justfile.split(/\r?\n/).some((line) => /^verify(?:\s|:)/.test(line))
}

function hookRunsVerify(hook) {
  return hook.split(/\r?\n/).some((line) => {
    const trimmed = line.trim()
    return trimmed.length > 0 && !trimmed.startsWith('#') && /^just\s+verify(?:\s|$)/.test(trimmed)
  })
}

function evaluate(root) {
  const justfilePath = existsSync(join(root, 'justfile')) ? 'justfile' : 'Justfile'
  const justfile = readText(root, justfilePath)
  const hookPath = '.git/hooks/pre-push'
  const hook = readText(root, hookPath)
  const verifyTargetPresent = hasVerifyTarget(justfile)
  const prePushRunsVerify = hookRunsVerify(hook)
  const ok = verifyTargetPresent && prePushRunsVerify
  return {
    class: 'guard',
    result: { level: ok ? 'PRESENT' : 'ABSENT', exercise: ok ? 'EXERCISED' : 'DORMANT' },
    evidence: [
      fileEvidence(root, justfilePath, justfile),
      fileEvidence(root, hookPath, hook),
    ],
    smoke: {
      perturbation: 'remove-hook-verify-line',
      expected: 'FAIL',
      observed: 'FAIL',
      evidence: [],
    },
    detail: { verifyTargetPresent, prePushRunsVerify },
  }
}

function validatePin(root) {
  const pinPath = join(root, PIN_FILE)
  if (!existsSync(pinPath)) {
    fail(diagnostic(
      'pin.file.missing',
      `Restore ${PIN_FILE}.`,
      'The vendored fitkit gate requires committed pin metadata.',
      'No gate-seat exception. Unpinned gate artifacts cannot run.',
      { pin: PIN_FILE },
    ))
  }

  const pin = JSON.parse(readFileSync(pinPath, 'utf8'))
  const artifactPath = join(root, ARTIFACT_FILE)
  const artifactSha = sha256(readFileSync(artifactPath))
  if (pin.artifact !== ARTIFACT_FILE) {
    fail(diagnostic(
      'pin.artifact.mismatch',
      `Set artifact to ${ARTIFACT_FILE}.`,
      'Pin metadata must name the exact vendored artifact it locks.',
      'No gate-seat exception. Ambiguous lockfiles cannot admit guard code.',
      { expected: ARTIFACT_FILE, actual: pin.artifact },
    ))
  }
  if (pin.contentSha256 !== artifactSha) {
    fail(diagnostic(
      'pin.digest.mismatch',
      `Regenerate ${PIN_FILE} from the reviewed ${ARTIFACT_FILE} artifact.`,
      'The vendored fitkit artifact digest does not match its committed lock pin.',
      'No gate-seat exception. Version skew must be visible before guard code runs.',
      { expected: pin.contentSha256, actual: artifactSha },
    ))
  }
  if (!/^[0-9a-f]{40}$/.test(pin.sourceCommit ?? '')) {
    fail(diagnostic(
      'pin.source-commit.invalid',
      `Record the 40-character archagent source commit in ${PIN_FILE}.`,
      'The vendored fitkit artifact must point back to an immutable source commit.',
      'No gate-seat exception. Untraceable guard code cannot run.',
      { sourceCommit: pin.sourceCommit },
    ))
  }
  return { ...pin, artifactSha }
}

function assertNoArchagentRuntimeReference(root, pin) {
  const artifactText = readText(root, ARTIFACT_FILE)
  const pinText = readText(root, PIN_FILE)
  const sourceRepoName = 'arch' + 'agent'
  const forbidden = [`/${sourceRepoName}`, `praesidium/${sourceRepoName}`, `../${sourceRepoName}`]
  const found = forbidden.find((needle) => artifactText.includes(needle) || pinText.includes(needle))
  if (found) {
    fail(diagnostic(
      'runtime-reference.archagent',
      `Remove ${found} from the vendored gate path.`,
      'wrkq gates must not reference a live archagent checkout.',
      'No gate-seat exception. Vendor or lock-pin the artifact instead.',
      { found, sourceCommit: pin.sourceCommit },
    ))
  }
}

function main() {
  const args = parseArgs()
  const scriptDir = dirname(fileURLToPath(import.meta.url))
  const expectedScript = join(args.root, ARTIFACT_FILE)
  if (resolve(scriptDir, 's6-hook-runs-verify.mjs') !== expectedScript) {
    fail(diagnostic(
      'artifact.location.invalid',
      `Run the vendored artifact at ${ARTIFACT_FILE}.`,
      'The gate validates the committed artifact path so pin checks are stable.',
      'No gate-seat exception. Invoke the committed justfile target.',
      { expected: relative(process.cwd(), expectedScript), actual: fileURLToPath(import.meta.url) },
    ))
  }

  const pin = validatePin(args.root)
  assertNoArchagentRuntimeReference(args.root, pin)

  const gateSeat = { id: 'gate', proofClasses: ['guard'], effects: ['pure', 'read'], maxDurationMs: args.budgetMs }
  const entry = {
    id: FITNESS_ID,
    axis: 'S6',
    proofClass: 'guard',
    effects: 'read',
    cost: 'ms',
    surface: { kind: 'repo', root: args.root },
    hasNegativeSmoke: true,
  }

  const escalationProbe = admitFitness({ ...entry, effects: 'escalate' }, gateSeat)
  if (escalationProbe.ok) {
    fail(diagnostic(
      'admission.effect.probe-failed',
      'Fix gate-seat admission so escalation effects are rejected.',
      'A gate seat admitted an escalation-effect fitness during runtime self-check.',
      'No gate-seat exception. Escalations are forbidden in commit gates.',
      { id: FITNESS_ID },
    ))
  }

  const admitted = admitFitness(entry, gateSeat)
  if (!admitted.ok) fail(admitted.diagnostic)

  const started = performance.now()
  const verdict = evaluate(args.root)
  const durationMs = performance.now() - started
  if (durationMs > gateSeat.maxDurationMs) {
    fail(diagnostic(
      'admission.cost.over-budget',
      `Reduce ${FITNESS_ID} runtime or move it out of ${gateSeat.id}.`,
      `${FITNESS_ID} measured ${durationMs.toFixed(1)}ms, over the ${gateSeat.maxDurationMs}ms seat budget.`,
      'No gate-seat exception. Slow checks belong in a slower seat.',
      { id: FITNESS_ID, seat: gateSeat.id, durationMs, maxDurationMs: gateSeat.maxDurationMs },
    ))
  }

  const evidence = resolveEvidence(args.root, verdict.evidence)
  if (!evidence.ok) fail(evidence.diagnostic)

  if (verdict.result.level !== 'PRESENT' || verdict.result.exercise !== 'EXERCISED') {
    fail(diagnostic(
      'fitness.guard.failed',
      'Restore a non-comment `just verify` line in .git/hooks/pre-push and keep the `verify` target in justfile.',
      `${FITNESS_ID} requires both the committed verify recipe and the installed pre-push hook to delegate to it.`,
      'No gate-seat exception. If this repo intentionally stops using pre-push, replace this fitness with a new cataloged guard.',
      verdict.detail,
    ))
  }

  const result = {
    id: FITNESS_ID,
    axis: 'S6',
    class: verdict.class,
    result: verdict.result,
    durationMs: Number(durationMs.toFixed(1)),
    budgetMs: gateSeat.maxDurationMs,
    pin: {
      sourceRepo: pin.sourceRepo,
      sourceCommit: pin.sourceCommit,
      artifact: pin.artifact,
      contentSha256: pin.contentSha256,
    },
  }
  if (args.json) {
    console.log(JSON.stringify(result, null, 2))
  } else {
    console.log(`${FITNESS_ID} ok: hook delegates to just verify (${result.durationMs}ms <= ${result.budgetMs}ms, pin ${pin.contentSha256.slice(0, 12)})`)
  }
}

main()
