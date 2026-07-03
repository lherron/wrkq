#!/usr/bin/env node
import { createHash } from 'node:crypto'
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { performance } from 'node:perf_hooks'

const FITNESS_ID = 'fit:s6/hook-runs-verify'
const DEFAULT_BUDGET_MS = 100
const PIN_FILE = 'tools/fitkit/s6-hook-runs-verify.pin.json'
const ARTIFACT_FILE = 'tools/fitkit/s6-hook-runs-verify.mjs'
const CONDUCTOR_VERSION = 'fitkit-conductor@1'

const proofClasses = ['presence', 'guard', 'usage', 'judgment']
const effects = ['pure', 'read', 'agent', 'sandbox-mutate', 'escalate', 'repo-mutate:mechanical', 'repo-mutate:design']
const costHints = ['ms', 'seconds', 'agent-turn']
const schemaVersions = {
  fact: 'fact@1',
  runtimeEntry: 'runtime-entry@1',
  verdict: 'verdict@1',
}
const extractorVersions = {
  taskRunner: 'facts.taskRunner@1',
  hooks: 'facts.hooks@1',
}
const definition = {
  id: FITNESS_ID,
  version: '2026-07-03.conductor.1',
  axis: 'S6',
  proofClass: 'guard',
  effects: 'read',
  cost: 'ms',
  surface: { kind: 'repo' },
  facts: ['taskRunner', 'hooks'],
  hasNegativeSmoke: true,
}

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

function ok(value) {
  return { ok: true, value }
}

function err(code, fix, why, exception, detail) {
  return { ok: false, diagnostic: diagnostic(code, fix, why, exception, detail) }
}

function parseFitnessEntry(input) {
  if (!isObject(input)) {
    return err('admission.entry.malformed', 'Provide a registry object.', 'Seat admission received a non-object registry entry.', 'No automatic exception. Use the owning profile waiver or manual review channel.')
  }
  if (typeof input.id !== 'string' || input.id.length === 0) {
    return err('admission.entry.id', 'Set a non-empty string id.', 'Fitness entries require stable string IDs.', 'No automatic exception. Use the owning profile waiver or manual review channel.')
  }
  if (typeof input.axis !== 'string' || input.axis.length === 0) {
    return err('admission.entry.axis', 'Set a non-empty string axis.', 'Axes bind by string ID and must be explicit.', 'No automatic exception. Use the owning profile waiver or manual review channel.')
  }
  if (!proofClasses.includes(input.proofClass)) {
    return err('admission.entry.proof-class', 'Use proofClass presence|guard|usage|judgment.', 'Unknown proof classes cannot be admitted.', 'No automatic exception. Use the owning profile waiver or manual review channel.')
  }
  if (!effects.includes(input.effects)) {
    return err('admission.entry.effect', 'Use a declared effect enum value.', 'Unknown effects cannot be placed safely in a seat.', 'No automatic exception. Use the owning profile waiver or manual review channel.')
  }
  if (!costHints.includes(input.cost)) {
    return err('admission.entry.cost', 'Use cost ms|seconds|agent-turn.', 'Unknown cost hints cannot be routed.', 'No automatic exception. Use the owning profile waiver or manual review channel.')
  }
  if (!isObject(input.surface) || !['repo', 'package', 'dir'].includes(input.surface.kind)) {
    return err('admission.entry.surface', 'Attach a repo|package|dir surface.', 'Fitness entries must declare their scope.', 'No automatic exception. Use the owning profile waiver or manual review channel.')
  }
  if (input.proofClass === 'guard' && input.hasNegativeSmoke !== true) {
    return err('admission.guard.no-smoke', 'Attach a negative smoke perturbation proof to the guard fitness.', 'A guard verdict without fires-on-bad proof cannot claim guard closure.', 'Mark the row manual residue until negative smoke exists.', { id: input.id })
  }
  return ok(input)
}

function admitFitness(entryInput, seat) {
  const parsed = parseFitnessEntry(entryInput)
  if (!parsed.ok) return parsed

  const entry = parsed.value
  if (!seat.proofClasses.includes(entry.proofClass)) {
    return err(
      'admission.proof-class.rejected',
      `Move ${entry.id} to a seat that admits ${entry.proofClass} proof.`,
      `${seat.id} admits ${seat.proofClasses.join(', ')} proof, not ${entry.proofClass}.`,
      'Use the owning profile/manual-residue channel for exceptions.',
      { id: entry.id, seat: seat.id, proofClass: entry.proofClass },
    )
  }
  if (!seat.effects.includes(entry.effects)) {
    return err(
      'admission.effect.rejected',
      `Remove ${entry.effects} from ${seat.id} or run ${entry.id} in a seat with that authority.`,
      `${seat.id} cannot admit ${entry.effects}; gate seats are limited to pure/read authority.`,
      'No gate-seat exception. Escalate through ASSESS/APPLY/VERIFY instead.',
      { id: entry.id, seat: seat.id, effects: entry.effects },
    )
  }
  return ok({ entry })
}

function readOptional(root, path) {
  const absolute = join(root, path)
  return existsSync(absolute) ? readFileSync(absolute, 'utf8') : undefined
}

function readRequired(root, path) {
  const text = readOptional(root, path)
  if (text === undefined) {
    return err(
      'evidence.file.missing',
      `Create ${path} or restore it from the wrkq repo.`,
      `${FITNESS_ID} cannot resolve required file evidence ${path}.`,
      'No gate-seat exception. Guard truth must be locally inspectable.',
      { path },
    )
  }
  return ok(text)
}

function fileEvidence(path, content) {
  return { kind: 'file', path, contentSha: sha256(content) }
}

function findJustfile(root) {
  if (existsSync(join(root, 'justfile'))) return 'justfile'
  if (existsSync(join(root, 'Justfile'))) return 'Justfile'
  return 'Justfile'
}

function parseJustTargets(content) {
  const targets = {}
  const lines = content.split(/\r?\n/)
  for (const [index, line] of lines.entries()) {
    const match = line.match(/^([A-Za-z0-9_-]+)(?:\s+[^:#]+)?\s*:/)
    if (!match?.[1]) continue
    targets[match[1]] = { name: match[1], line: index + 1 }
  }
  return targets
}

function taskRunner(root) {
  const path = findJustfile(root)
  const text = readRequired(root, path)
  if (!text.ok) return text
  return ok({
    value: { kind: 'just', targets: parseJustTargets(text.value) },
    evidence: [fileEvidence(path, text.value)],
    extractedAt: new Date().toISOString(),
    extractorVersion: extractorVersions.taskRunner,
    dependencies: [{ kind: 'file', path }],
    surface: { kind: 'repo', root },
  })
}

function hooks(root) {
  const path = '.git/hooks/pre-push'
  const text = readOptional(root, path)
  return ok({
    value: {
      hooks: text === undefined ? [] : [{ path, kind: 'git', content: text }],
    },
    evidence: text === undefined ? [] : [fileEvidence(path, text)],
    extractedAt: new Date().toISOString(),
    extractorVersion: extractorVersions.hooks,
    dependencies: text === undefined ? [{ kind: 'unknown', reason: `${path} missing` }] : [{ kind: 'file', path }],
    surface: { kind: 'repo', root },
  })
}

function extractFacts(root) {
  const facts = []
  for (const extractor of [taskRunner, hooks]) {
    const fact = extractor(root)
    if (!fact.ok) return fact
    facts.push(fact.value)
  }
  return ok(facts)
}

function hasVerifyTarget(runnerFact) {
  return Object.prototype.hasOwnProperty.call(runnerFact.value.targets, 'verify')
}

function hookRunsVerify(hookFact) {
  return hookFact.value.hooks.some((hook) => hook.content.split(/\r?\n/).some((line) => {
    const trimmed = line.trim()
    return trimmed.length > 0 && !trimmed.startsWith('#') && /^just\s+verify(?:\s|$)/.test(trimmed)
  }))
}

function evaluate(facts) {
  const runner = facts[0]
  const hookFact = facts[1]
  const verifyTargetPresent = hasVerifyTarget(runner)
  const prePushRunsVerify = hookRunsVerify(hookFact)
  const passed = verifyTargetPresent && prePushRunsVerify
  return {
    class: 'guard',
    result: { level: passed ? 'PRESENT' : 'ABSENT', exercise: passed ? 'EXERCISED' : 'DORMANT' },
    evidence: [...runner.evidence, ...hookFact.evidence],
    smoke: {
      perturbation: 'remove-hook-verify-line',
      expected: 'FAIL',
      observed: 'FAIL',
      evidence: [],
    },
    ttl: 'per-run',
    detail: { verifyTargetPresent, prePushRunsVerify },
  }
}

function validateVerdictShape(verdict) {
  if (!isObject(verdict)) {
    return err('verdict.malformed', 'Return a verdict object.', 'Seat runtime validation received a non-object verdict.', 'No gate-seat exception. Malformed verdicts cannot close guard truth.')
  }
  if (verdict.class !== 'guard') {
    return err('verdict.class.mismatch', 'Return a guard verdict from this fitness.', 'The runtime verdict class must match the proof class admitted by the gate.', 'No gate-seat exception. Mismatched proof cannot close guard truth.', { expectedClass: 'guard', actualClass: verdict.class })
  }
  if (!['ABSENT', 'PARTIAL', 'PRESENT'].includes(verdict.result?.level)) {
    return err('verdict.result.level', 'Set result.level to ABSENT|PARTIAL|PRESENT.', 'Unknown score levels cannot participate in the fitkit lattice.', 'No gate-seat exception. Malformed scores cannot close guard truth.')
  }
  if (verdict.result.exercise !== undefined && !['DORMANT', 'EXERCISED'].includes(verdict.result.exercise)) {
    return err('verdict.result.exercise', 'Set result.exercise to DORMANT or EXERCISED, or omit it.', 'Unknown exercise states cannot participate in the fitkit lattice.', 'No gate-seat exception. Malformed scores cannot close guard truth.')
  }
  if (!Array.isArray(verdict.evidence)) {
    return err('verdict.evidence.malformed', 'Return evidence as an array.', 'Runtime evidence validation requires an evidence array.', 'No gate-seat exception. Malformed evidence cannot close guard truth.')
  }
  if (!isObject(verdict.smoke) || verdict.smoke.expected !== 'FAIL' || verdict.smoke.observed !== 'FAIL') {
    return err('verdict.guard.smoke-malformed', 'Attach a FAIL/FAIL guard smoke proof before accepting the verdict.', 'Guard closure requires a structured negative smoke proof at runtime.', 'No gate-seat exception. Missing smoke cannot close guard truth.')
  }
  return ok(verdict)
}

function resolveEvidence(root, evidence) {
  for (const item of evidence) {
    if (item.kind !== 'file' || typeof item.path !== 'string' || typeof item.contentSha !== 'string') {
      return err(
        'evidence.entry.malformed',
        'Emit file evidence with kind, path, and contentSha.',
        `${FITNESS_ID} emitted malformed evidence.`,
        'No gate-seat exception. Malformed evidence cannot close guard truth.',
        { evidence: item },
      )
    }
    const content = readRequired(root, item.path)
    if (!content.ok) return content
    const actual = sha256(content.value)
    if (actual !== item.contentSha) {
      return err(
        'evidence.file.changed',
        `Rerun ${FITNESS_ID} after ${item.path} settles.`,
        `${item.path} content changed while resolving evidence.`,
        'No gate-seat exception. Stale evidence cannot close guard truth.',
        { path: item.path, expected: item.contentSha, actual },
      )
    }
  }
  return ok(undefined)
}

function validateVerdictEvidence(root, verdict) {
  const shaped = validateVerdictShape(verdict)
  if (!shaped.ok) return shaped
  const evidence = resolveEvidence(root, shaped.value.evidence)
  if (!evidence.ok) return evidence
  return shaped
}

function cloneSmokeSandbox(root) {
  const sandboxRoot = mkdtempSync(join(tmpdir(), 'fitkit-s6-smoke-'))
  const justfile = findJustfile(root)
  if (existsSync(join(root, justfile))) cpSync(join(root, justfile), join(sandboxRoot, justfile))
  const hookSource = join(root, '.git/hooks/pre-push')
  if (existsSync(hookSource)) {
    mkdirSync(join(sandboxRoot, '.git/hooks'), { recursive: true })
    cpSync(hookSource, join(sandboxRoot, '.git/hooks/pre-push'))
  }
  return sandboxRoot
}

function removeHookVerifyLine(sandboxRoot) {
  const hookPath = join(sandboxRoot, '.git/hooks/pre-push')
  if (!existsSync(hookPath)) return
  const original = readFileSync(hookPath, 'utf8')
  writeFileSync(hookPath, original.replace(/^(?!\s*#).*\bjust\s+verify\b.*$/gm, '# fitkit perturbation: just verify stripped'))
}

async function runNegativeSmoke(root, context) {
  const sandboxRoot = cloneSmokeSandbox(root)
  try {
    removeHookVerifyLine(sandboxRoot)
    const result = await evaluateFitness({ ...context, root: sandboxRoot, mode: 'smoke', skipPin: true })
    if (!result.accepted) {
      return err(
        'perturbation.negative-smoke.failed',
        `Repair ${FITNESS_ID} negative smoke execution before accepting the guard verdict.`,
        'The guard perturbation could not produce a valid conductor verdict in the sandbox.',
        'No gate-seat exception. Guard truth requires a runnable smoke proof.',
        { cause: result.diagnostic },
      )
    }
    const observed = result.verdict.result.level === 'PRESENT' ? 'PASS' : 'FAIL'
    if (observed !== 'FAIL') {
      return err(
        'perturbation.negative-smoke.mismatch',
        `Repair ${FITNESS_ID} or its perturbation so removing the hook line fails the guard.`,
        'Guard closure requires the declared negativeSmoke perturbation to produce the recorded fires-on-bad outcome.',
        'No gate-seat exception. Guard truth must fail on the bad fixture.',
        { expected: 'FAIL', observed, perturbation: 'remove-hook-verify-line' },
      )
    }
    return ok({ perturbation: 'remove-hook-verify-line', expected: 'FAIL', observed: 'FAIL', evidence: [] })
  } finally {
    rmSync(sandboxRoot, { recursive: true, force: true })
  }
}

function runtimeEntry(root) {
  return {
    ...definition,
    surface: { kind: 'repo', root },
  }
}

function memoKey(root, seat) {
  return JSON.stringify({
    conductorVersion: CONDUCTOR_VERSION,
    fitness: {
      id: definition.id,
      version: definition.version,
      digest: sha256(JSON.stringify(definition)),
    },
    extractors: definition.facts.map((id) => ({ id, version: extractorVersions[id] })),
    seatPolicy: sha256(JSON.stringify(seat)),
    root,
    surface: { kind: 'repo', root },
    schemaVersions,
  })
}

export async function evaluateFitness(context) {
  const started = performance.now()
  const seat = context.seat ?? { id: 'gate', proofClasses: ['guard'], effects: ['pure', 'read'], maxDurationMs: context.budgetMs ?? DEFAULT_BUDGET_MS }

  const admitted = admitFitness(runtimeEntry(context.root), seat)
  if (!admitted.ok) return { accepted: false, diagnostic: admitted.diagnostic }

  const facts = extractFacts(context.root)
  if (!facts.ok) return { accepted: false, durationMs: performance.now() - started, diagnostic: facts.diagnostic }

  const verdictRaw = evaluate(facts.value, { root: context.root })
  const shaped = validateVerdictShape(verdictRaw)
  if (!shaped.ok) return { accepted: false, durationMs: performance.now() - started, facts: facts.value, diagnostic: shaped.diagnostic }

  const mainDurationMs = performance.now() - started
  if (mainDurationMs > seat.maxDurationMs) {
    return {
      accepted: false,
      durationMs: mainDurationMs,
      facts: facts.value,
      diagnostic: diagnostic(
        'admission.cost.over-budget',
        `Reduce ${FITNESS_ID} runtime or move it out of ${seat.id}.`,
        `${FITNESS_ID} measured ${mainDurationMs.toFixed(1)}ms, over the ${seat.maxDurationMs}ms seat budget.`,
        'No gate-seat exception. Slow checks belong in a slower seat.',
        { id: FITNESS_ID, seat: seat.id, durationMs: mainDurationMs, maxDurationMs: seat.maxDurationMs },
      ),
    }
  }

  let verdict = shaped.value
  if (context.mode !== 'smoke') {
    const smoke = await runNegativeSmoke(context.root, { ...context, seat })
    if (!smoke.ok) return { accepted: false, durationMs: performance.now() - started, facts: facts.value, diagnostic: smoke.diagnostic }
    verdict = { ...verdict, smoke: smoke.value }
  }

  const resolved = validateVerdictEvidence(context.root, verdict)
  if (!resolved.ok) return { accepted: false, durationMs: performance.now() - started, facts: facts.value, diagnostic: resolved.diagnostic }

  const durationMs = performance.now() - started
  if (durationMs > seat.maxDurationMs) {
    return {
      accepted: false,
      durationMs,
      facts: facts.value,
      diagnostic: diagnostic(
        'admission.cost.over-budget',
        `Reduce ${FITNESS_ID} runtime or move it out of ${seat.id}.`,
        `${FITNESS_ID} measured ${durationMs.toFixed(1)}ms, over the ${seat.maxDurationMs}ms seat budget.`,
        'No gate-seat exception. Slow checks belong in a slower seat.',
        { id: FITNESS_ID, seat: seat.id, durationMs, maxDurationMs: seat.maxDurationMs },
      ),
    }
  }

  return {
    accepted: true,
    verdict: resolved.value,
    durationMs,
    facts: facts.value,
    memo: { reused: false, memoized: false, reason: 'disabled', key: memoKey(context.root, seat) },
    diagnostics: [],
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
  const artifactText = readRequired(root, ARTIFACT_FILE)
  if (!artifactText.ok) fail(artifactText.diagnostic)
  const pinText = readRequired(root, PIN_FILE)
  if (!pinText.ok) fail(pinText.diagnostic)
  const sourceRepoName = 'arch' + 'agent'
  const forbidden = [`/${sourceRepoName}`, `praesidium/${sourceRepoName}`, `../${sourceRepoName}`]
  const found = forbidden.find((needle) => artifactText.value.includes(needle) || pinText.value.includes(needle))
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

async function main() {
  const args = parseArgs()
  const scriptDir = dirname(fileURLToPath(import.meta.url))
  const expectedScript = join(args.root, ARTIFACT_FILE)
  let actualScriptReal
  let expectedScriptReal
  try {
    actualScriptReal = realpathSync(resolve(scriptDir, 's6-hook-runs-verify.mjs'))
    expectedScriptReal = realpathSync(expectedScript)
  } catch (error) {
    fail(diagnostic(
      'artifact.location.unresolvable',
      `Run the vendored artifact at ${ARTIFACT_FILE}.`,
      'The gate could not resolve the committed artifact path for pin checks.',
      'No gate-seat exception. Invoke the committed justfile target.',
      { expected: relative(process.cwd(), expectedScript), actual: fileURLToPath(import.meta.url), error: error instanceof Error ? error.message : String(error) },
    ))
  }
  if (actualScriptReal !== expectedScriptReal) {
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
  const escalationProbe = admitFitness({ ...runtimeEntry(args.root), effects: 'escalate' }, gateSeat)
  if (escalationProbe.ok) {
    fail(diagnostic(
      'admission.effect.probe-failed',
      'Fix gate-seat admission so escalation effects are rejected.',
      'A gate seat admitted an escalation-effect fitness during runtime self-check.',
      'No gate-seat exception. Escalations are forbidden in commit gates.',
      { id: FITNESS_ID },
    ))
  }

  const result = await evaluateFitness({ root: args.root, budgetMs: args.budgetMs, seat: gateSeat })
  if (!result.accepted) fail(result.diagnostic)

  if (result.verdict.result.level !== 'PRESENT' || result.verdict.result.exercise !== 'EXERCISED') {
    fail(diagnostic(
      'fitness.guard.failed',
      'Restore a non-comment `just verify` line in .git/hooks/pre-push and keep the `verify` target in justfile.',
      `${FITNESS_ID} requires both the committed verify recipe and the installed pre-push hook to delegate to it.`,
      'No gate-seat exception. If this repo intentionally stops using pre-push, replace this fitness with a new cataloged guard.',
      result.verdict.detail,
    ))
  }

  const output = {
    id: FITNESS_ID,
    axis: 'S6',
    class: result.verdict.class,
    result: result.verdict.result,
    durationMs: Number(result.durationMs.toFixed(1)),
    budgetMs: gateSeat.maxDurationMs,
    conductorVersion: CONDUCTOR_VERSION,
    memo: result.memo,
    pin: {
      sourceRepo: pin.sourceRepo,
      sourceCommit: pin.sourceCommit,
      artifact: pin.artifact,
      contentSha256: pin.contentSha256,
    },
  }
  if (args.json) {
    console.log(JSON.stringify(output, null, 2))
  } else {
    console.log(`${FITNESS_ID} ok: hook delegates to just verify (${output.durationMs}ms <= ${output.budgetMs}ms, pin ${pin.contentSha256.slice(0, 12)})`)
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
