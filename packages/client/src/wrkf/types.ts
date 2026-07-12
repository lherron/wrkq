/**
 * wrkf/types.ts — DTOs for the wrkf namespace (templates, instances, evidence,
 * obligations, checks, hooks, transitions, runs, effects).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7. All RPC DTO JSON fields are
 * camelCase. Field sets verified against the live `wrkf rpc --stdio` server
 * (proto 2026-06-30).
 *
 * Several wrkf result envelopes return broad workflow-runtime structs; we type
 * the well-known fields and allow extra keys (`[k: string]: unknown`) rather
 * than pin a field that may not exist for every template/state.
 */

// ── State ────────────────────────────────────────────────────────────────────

/**
 * Workflow state. The server returns a structured `{ status, phase, outcome }`
 * object; some envelopes carry a bare state string. Both are accepted.
 */
export type WrkfState =
  | string
  | {
      status?: string;
      phase?: string;
      outcome?: string;
      [k: string]: unknown;
    };

// ── Core resources ───────────────────────────────────────────────────────────

export interface WrkfInstance {
  id: string;
  taskUuid?: string;
  taskRef?: string;
  projectId?: string;
  templateId?: string;
  templateVersion?: string;
  templateHash?: string;
  status?: string;
  phase?: string;
  outcome?: string;
  revision: number;
  taskDocEtag?: string;
  taskDocHash?: string;
  createdAt?: string;
  updatedAt?: string;
  [k: string]: unknown;
}

// ── Immutable instance ledger ───────────────────────────────────────────────

/** Platform lifecycle kinds are documented, while workflow-owned kinds remain open. */
export type LedgerKind =
  | "runner_start"
  | "runner_exit"
  | "park"
  | "resume"
  | "operator_intervention"
  | "escalation"
  | "strike"
  | "close_scorecard"
  | (string & {});

/** Platform artifact vocabulary carried inside a ledger body's free-form refs. */
export interface LedgerRefs {
  comments?: string[];
  dms?: string[];
  evidence?: string[];
  logs?: Record<string, string>;
  pids?: string[];
  runtime?: string[];
  trace_id?: string;
  artifacts_dir?: string;
  template?: string;
}

export interface LedgerEntry {
  seq: number;
  uuid: string;
  instanceId: string;
  taskId: string;
  ts: string;
  kind: LedgerKind;
  aboutPrincipalRef: string;
  writtenBy: string;
  body: Record<string, unknown> & { refs?: LedgerRefs };
}

export interface LedgerAppendInput {
  taskId: string;
  kind: LedgerKind;
  aboutPrincipalRef: string;
  body?: Record<string, unknown> & { refs?: LedgerRefs };
}

export interface LedgerListFilter {
  taskId?: string;
  aboutPrincipalRef?: string;
  kind?: LedgerKind;
  since?: string;
  until?: string;
  limit?: number;
  cursor?: string;
}

export interface LedgerListResult {
  entries: LedgerEntry[];
  nextCursor?: string;
}

export interface WrkfEvent {
  id: string;
  type?: string;
  payload?: {
    from?: WrkfState;
    to?: WrkfState;
    transition?: string;
    outcome?: string;
    [k: string]: unknown;
  };
  [k: string]: unknown;
}

export interface WrkfEvidenceBuild {
  id?: string;
  version?: string;
  env?: string;
}

export interface WrkfEvidence {
  id: string;
  instanceId?: string;
  kind?: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  data?: unknown;
  principal_ref?: string;
  role?: string;
  /** Persisted run linkage; see docs/wrkq-wrkf-rpc.md §9.7. */
  runId?: string;
  contentHash?: string;
  build?: WrkfEvidenceBuild;
  producedAt?: string;
  [k: string]: unknown;
}

export interface WrkfObligation {
  id: string;
  kind?: string;
  status?: string;
  ownerRole?: string;
  blocking?: boolean;
  reason?: string;
  [k: string]: unknown;
}

export interface WrkfEffect {
  id: string;
  instanceId?: string;
  kind?: string;
  status?: string;
  role?: string;
  payload?: Record<string, unknown>;
  adapter?: string;
  revision?: number;
  sequence?: number;
  attempts?: number;
  idempotencyKey?: string;
  semanticKey?: string;
  leaseToken?: string;
  leasedBy?: string;
  leasedUntil?: string;
  createdAt?: string;
  updatedAt?: string;
  [k: string]: unknown;
}

export interface WrkfRun {
  id: string;
  instanceId?: string;
  status?: string;
  role?: string;
  principal_ref?: string;
  externalRunRef?: string;
  deliveryRef?: string;
  lane?: string;
  startedAt?: string;
  finishedAt?: string;
  terminalResult?: string;
  [k: string]: unknown;
}

export interface WrkfCheckRun {
  id: string;
  verdict?: string;
  outcome?: string;
  [k: string]: unknown;
}

export interface WrkfWorkflowTemplateSummary {
  id: string;
  version: string;
  hash: string;
  kind?: string;
  description?: string;
  installedAt?: string;
  installedBy?: string;
  [k: string]: unknown;
}

// ── Template registry ────────────────────────────────────────────────────────

export interface WrkfWorkflowValidateParams {
  path: string;
}

export interface WrkfWorkflowValidateResult {
  valid: boolean;
  [k: string]: unknown;
}

export interface WrkfWorkflowShowParams {
  ref: string;
}

export interface WrkfWorkflowShowResult {
  template: Record<string, unknown>;
  hash: string;
}

export interface WrkfWorkflowListParams {
  [k: string]: unknown;
}

export interface WrkfWorkflowListResult {
  templates: WrkfWorkflowTemplateSummary[];
}

export interface WrkfWorkflowDiffParams {
  oldPath: string;
  newPath: string;
}

export interface WrkfDiffResult {
  old: WrkfWorkflowTemplateSummary;
  new: WrkfWorkflowTemplateSummary;
  sameHash: boolean;
  [k: string]: unknown;
}

export interface WrkfWorkflowInstallParams {
  path: string;
}

export interface WrkfInstallResult {
  id: string;
  version: string;
  hash: string;
  installed: boolean;
}

// ── Instance state access ────────────────────────────────────────────────────

export interface WrkfInstanceShowParams {
  instanceId?: string;
  task?: string;
}

export interface WrkfInstanceNextParams {
  instanceId?: string;
  task?: string;
  role?: string;
}

export interface WrkfNextResult {
  instance?: Record<string, unknown>;
  actions: Array<{ kind: string; [k: string]: unknown }>;
  blockedTransitions?: unknown[];
  openObligations?: unknown[];
  pendingEffects?: unknown[];
  [k: string]: unknown;
}

// ── Evidence ─────────────────────────────────────────────────────────────────

export interface WrkfEvidenceAddParams {
  task?: string;
  instanceId?: string;
  kind: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  data?: unknown;
  principal_ref?: string;
  role?: string;
  /** Persisted run linkage; see docs/wrkq-wrkf-rpc.md §9.7. */
  runId?: string;
  contentHash?: string;
  build?: WrkfEvidenceBuild;
  idempotencyKey?: string;
}

export interface WrkfEvidenceListParams {
  task?: string;
  instanceId?: string;
}

export interface WrkfEvidenceShowParams {
  id: string;
}

export interface WrkfEvidenceSuggestParams {
  task?: string;
  instanceId?: string;
  transition: string;
}

// ── Role bindings ───────────────────────────────────────────────────────────

export interface WrkfRoleBinding {
  instanceId: string;
  role: string;
  principal_ref: string;
  deliveryRef?: string;
  lane?: string;
  bindingMode: string;
  boundAt: string;
}

export interface WrkfRoleListParams {
  task?: string;
  instanceId?: string;
}

export interface WrkfRoleBindParams {
  task?: string;
  instanceId?: string;
  role: string;
  principal_ref: string;
  deliveryRef?: string;
  lane?: string;
  bindingMode?: "required" | "optional" | "auto" | string;
}

export interface WrkfRoleUnbindParams {
  task?: string;
  instanceId?: string;
  role: string;
  principal_ref?: string;
}

export interface WrkfRoleSetParams {
  task?: string;
  instanceId?: string;
  roleMap: Record<string, string>;
}

export interface WrkfEventQueryParams {
  eventType?: "workflow.transitioned" | string;
  project?: string;
  fromPhase?: string;
  toPhase?: string;
  riskClass?: string;
  riskClasses?: string[];
  excludeRiskClass?: string;
  excludeRiskClasses?: string[];
  boundRole?: string;
  includeRoleBindings?: boolean;
  limit?: number;
  cursor?: string;
}

export interface WrkfTransitionEventTask {
  uuid: string;
  id: string;
  slug?: string;
  ref?: string;
  projectUuid?: string;
  projectId?: string;
  projectSlug?: string;
  riskClass?: string;
}

export interface WrkfTransitionEvent {
  id: string;
  eventType: "workflow.transitioned" | string;
  instanceId: string;
  seq: number;
  task: WrkfTransitionEventTask;
  transition?: string;
  outcome?: string;
  from?: WrkfState;
  to?: WrkfState;
  fromPhase?: string;
  toPhase?: string;
  transitionedAt: string;
  principal_ref?: string;
  role?: string;
  matchingRoleBindings: WrkfRoleBinding[];
  roleBindings?: WrkfRoleBinding[];
  payload?: {
    from?: WrkfState;
    to?: WrkfState;
    transition?: string;
    outcome?: string;
    [k: string]: unknown;
  };
  [k: string]: unknown;
}

export interface WrkfEventQueryResult {
  items: WrkfTransitionEvent[];
  nextCursor?: string;
  hasMore: boolean;
}

export interface WrkfSuggestResult {
  transition: string;
  required: unknown[];
  missing: string[];
  checks: string[];
  warnings: string[];
  [k: string]: unknown;
}

// ── Obligations ──────────────────────────────────────────────────────────────

export interface WrkfObligationListParams {
  task?: string;
  instanceId?: string;
}

export interface WrkfObligationShowParams {
  id: string;
}

export interface WrkfObligationSatisfyParams {
  task?: string;
  instanceId?: string;
  id: string;
  evidenceId?: string;
  idempotencyKey?: string;
}

export interface WrkfObligationWaiveParams {
  task?: string;
  instanceId?: string;
  id: string;
  reason?: string;
  idempotencyKey?: string;
}

export interface WrkfObligationCancelParams {
  task?: string;
  instanceId?: string;
  id: string;
  reason?: string;
  idempotencyKey?: string;
}

// ── Checks and hooks ─────────────────────────────────────────────────────────

export interface WrkfCheckPreflightParams {
  task?: string;
  instanceId?: string;
  transition: string;
}

export interface WrkfCheckRunParams {
  task?: string;
  instanceId?: string;
  transition: string;
}

export interface WrkfCheckRunResult {
  runs: WrkfCheckRun[];
  [k: string]: unknown;
}

export interface WrkfCheckShowParams {
  id: string;
}

export interface WrkfCheckListParams {
  task?: string;
  instanceId?: string;
  transition?: string;
}

export interface WrkfHookListParams {
  [k: string]: unknown;
}

export interface WrkfHookShowParams {
  id: string;
}

export interface WrkfHookRunParams {
  task?: string;
  instanceId?: string;
  transition: string;
  hookId: string;
}

// ── Transitions ──────────────────────────────────────────────────────────────

export interface WrkfTransitionApplyParams {
  task?: string;
  instanceId?: string;
  transition: string;
  role?: string;
  principal_ref?: string;
  /** CAS precondition; see docs/wrkq-wrkf-rpc.md §8.3. */
  expectRevision?: number;
  idempotencyKey?: string;
  runChecks?: boolean;
  dryRun?: boolean;
}

export interface WrkfTransitionResult {
  task: string;
  instanceId: string;
  state: WrkfState;
  revision: number;
  eventId: string;
  effects: WrkfEffect[];
  obligations: WrkfObligation[];
}

// ── Suspension resolution ────────────────────────────────────────────────────

/** Dispositions accepted by `wrkf.suspension.resolve`. */
export type WrkfSuspensionDisposition = "resume" | "close" | "cancel";

/**
 * `wrkf.suspension.resolve` input. The matching `suspensionId` is the ONLY gate
 * (Lance ruling, 2026-07-12): no role checks, no evidence validation.
 * `explanation` is recorded free text; `expectRevision` is the ordinary CAS
 * precondition (docs/wrkq-wrkf-rpc.md §8.3).
 */
export interface WrkfSuspensionResolveParams {
  suspensionId: string;
  disposition: WrkfSuspensionDisposition;
  explanation?: string;
  expectRevision?: number;
  role?: string;
  principal_ref?: string;
}

export interface WrkfSuspensionResolveResult {
  task?: string;
  instanceId: string;
  suspensionId: string;
  disposition: WrkfSuspensionDisposition;
  state: WrkfState;
  revision: number;
  eventId: string;
  effects: WrkfEffect[];
  instance?: WrkfInstance;
}

// ── Runs ─────────────────────────────────────────────────────────────────────

export interface WrkfRunStartParams {
  task?: string;
  instanceId?: string;
  role?: string;
  principal_ref?: string;
  idempotencyKey?: string;
  deliveryRef?: string;
  lane?: string;
  externalRunRef?: string;
}

export interface WrkfRunBindExternalParams {
  runId: string;
  externalRunRef: string;
  deliveryRef?: string;
  lane?: string;
  idempotencyKey?: string;
}

export interface WrkfRunFinishParams {
  runId: string;
  summary?: string;
  [k: string]: unknown;
}

export interface WrkfRunFailParams {
  runId: string;
  summary?: string;
  [k: string]: unknown;
}

export interface WrkfRunShowParams {
  runId: string;
}

export interface WrkfRunListParams {
  task?: string;
  instanceId?: string;
}

// ── Actions (low-ceremony composition over run/evidence/transition) ──────────

/** Workflow template ref backing an action run. */
export interface WrkfActionWorkflowRef {
  id: string;
  version: string;
  hash?: string;
}

/** Canonical semantic view of one action run. */
export interface WrkfActionRun {
  actionRunId: string;
  runId: string;
  task: string;
  instanceId: string;
  workflow: WrkfActionWorkflowRef;
  action: string;
  role: string;
  principal_ref?: string;
  lane?: string;
  deliveryRef?: string;
  externalRunRef?: string;
  status: string;
  startedAt: string;
  completedAt?: string;
  terminalResult?: string;
  leaseOwner?: string;
  leaseToken?: string;
  leaseExpiresAt?: string;
  heartbeatAt?: string;
  evidenceIds?: string[];
  evidenceKinds?: string[];
  transitionEventIds?: string[];
  [k: string]: unknown;
}

export interface WrkfActionEvidenceInput {
  kind?: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  data?: Record<string, unknown>;
  contentHash?: string;
  idempotencyKey?: string;
}

export interface WrkfActionNextScope {
  project?: string;
  path?: string;
  recursive?: boolean;
  templates?: string[];
}

export interface WrkfActionNextFilters {
  actions?: string[];
  roles?: string[];
  statuses?: string[];
  phases?: string[];
  includeBlocked?: boolean;
  includeActiveRuns?: boolean;
}

export interface WrkfActionNextParams {
  task?: string;
  instanceId?: string;
  scope?: WrkfActionNextScope;
  filters?: WrkfActionNextFilters;
  limit?: number;
}

export interface WrkfActionSourceBinding {
  sourceRunId: string;
  sourceEvidenceId?: string;
  /** Opaque, lane-computed source identity. Consumers must not parse it. */
  sourceIdentity?: string;
  artifactRef?: string;
}

export interface WrkfActionCandidate {
  instanceId: string;
  task: string;
  semanticActionKey: string;
  action: string;
  transition: string;
  role: string;
  requiredEvidenceKind: string;
  expectedStateRevision: number;
  expectedState: WrkfState;
  expectedTaskDocHash?: string;
  inputHash?: string;
  source?: WrkfActionSourceBinding;
  handlerContract?: string;
  workspaceMode?: "none" | "read-only" | "exclusive" | (string & {});
  workspaceRef?: string;
  sideEffectClasses?: string[];
  rank: number;
  blocked?: boolean;
  blockedReason?: string;
  [k: string]: unknown;
}

export interface WrkfActionNextResult {
  candidates: WrkfActionCandidate[];
  nextCursor?: string;
}

export interface WrkfActionClaimPrefer {
  instanceId?: string;
  semanticActionKey?: string;
  action?: string;
}

export interface WrkfRunnerCapability {
  handlerContract?: string;
  handlerId?: string;
  handlerVersion?: string;
  actions?: string[];
  roles?: string[];
  sideEffectClasses?: string[];
  workspaceModes?: string[];
}

export interface WrkfActionClaimParams {
  task?: string;
  instanceId?: string;
  prefer?: WrkfActionClaimPrefer;
  runnerId: string;
  agentRef: string;
  scopeRef?: string;
  capabilities?: WrkfRunnerCapability[];
  leaseMs: number;
  workspaceRoot?: string;
  idempotencyKey?: string;
  /** Latest acknowledged predecessor, or null for a first-ever claim. */
  priorRun: string | null;
}

export interface WrkfWorkflowRunAttempt {
  id: string;
  instanceId: string;
  semanticActionKey: string;
  action: string;
  role: string;
  attempt: number;
  status: string;
  agentRef?: string;
  scopeRef?: string;
  handlerContract?: string;
  handlerId?: string;
  handlerVersion?: string;
  externalRunRef?: string;
  workspaceRef?: string;
  source?: WrkfActionSourceBinding;
  startedAt: string;
  completedAt?: string;
  terminalSummary?: string;
  predecessorRunId?: string;
  [k: string]: unknown;
}

export interface WrkfActionTaskBinding {
  uuid: string;
  ref: string;
  path?: string;
}

export interface WrkfActionRunAuthority {
  runnerId: string;
  ownerToken: string;
  ownerGeneration: number;
  leaseExpiresAt: string;
  claimedAt: string;
  heartbeatAt?: string;
}

export interface WrkfActionClaimEvidenceRecord {
  id: string;
  kind: string;
  ref: string;
  summary?: string;
  producedAt: string;
}

export interface WrkfActionClaimPredecessor {
  runId: string;
  owner?: string;
  claimedAt: string;
  heartbeatAt?: string;
  expiresAt?: string;
  settleStatus: string;
  terminalResult?: string;
  sideEffectClasses: string[];
  externalRunRef?: string;
  workspaceRef?: string;
  evidenceWritten: WrkfActionClaimEvidenceRecord[];
}

export interface WrkfFencedRunBinding {
  run: WrkfWorkflowRunAttempt;
  task: WrkfActionTaskBinding;
  instance: WrkfInstance;
  authority: WrkfActionRunAuthority;
}

export interface WrkfActionClaimResult {
  binding?: WrkfFencedRunBinding;
}

export interface WrkfActionSettleParams {
  actionRunId?: string;
  runId?: string;
  ownerToken?: string;
  ownerGeneration?: number;
  result:
    | "completed"
    | "semantic_blocked"
    | "operational_failed"
    | "operator_required"
    | "cancelled"
    | (string & {});
  evidence?: WrkfActionEvidenceInput;
  /** Transition id to apply, or false to skip; omit for executable action transition. */
  transition?: string | false;
  terminalSummary?: string;
}

export interface WrkfActionSettleResult {
  run: WrkfWorkflowRunAttempt;
  evidence?: WrkfEvidence;
  transition?: WrkfTransitionResult;
  effects?: WrkfEffect[];
  obligations?: WrkfObligation[];
}

export interface WrkfActionStartParams {
  task?: string;
  instanceId?: string;
  workflow?: string;
  action: "triage" | "implement" | "review" | "verify" | (string & {});
  role?: string;
  principal_ref?: string;
  lane?: string;
  deliveryRef?: string | Record<string, unknown>;
  externalRunRef?: string;
  idempotencyKey?: string;
  leaseOwner?: string;
  leaseMs?: number;
}

export interface WrkfActionBindExternalParams {
  actionRunId: string;
  externalRunRef: string;
  deliveryRef?: string | Record<string, unknown>;
  lane?: string;
  idempotencyKey?: string;
}

export interface WrkfActionCompleteParams {
  actionRunId: string;
  leaseToken?: string;
  evidence?: WrkfActionEvidenceInput;
  /** Transition id to apply, or false to skip; omit for default resolution. */
  transition?: string | false;
  transitionIdempotencyKey?: string;
  runSummary?: string;
}

export interface WrkfActionCompleteResult {
  run: WrkfActionRun;
  evidence?: WrkfEvidence;
  transition?: WrkfTransitionResult;
}

export interface WrkfActionFailParams {
  actionRunId: string;
  leaseToken?: string;
  summary: string;
  evidence?: WrkfActionEvidenceInput;
}

export interface WrkfActionHeartbeatParams {
  actionRunId: string;
  leaseToken: string;
  leaseMs?: number;
}

export interface WrkfActionShowParams {
  actionRunId: string;
}

export interface WrkfActionListParams {
  task?: string;
  instanceId?: string;
  includeClosedInstances?: boolean;
  status?: string;
  action?: string;
  limit?: number;
  cursor?: string;
}

export interface WrkfActionListResult {
  items: WrkfActionRun[];
}

// ── Effects ──────────────────────────────────────────────────────────────────

export interface WrkfEffectListParams {
  task?: string;
  instanceId?: string;
  all?: boolean;
}

export interface WrkfEffectShowParams {
  id: string;
}

export interface WrkfEffectClaimParams {
  adapter: string;
  limit: number;
  leaseMs: number;
  task?: string;
  instanceId?: string;
  kind?: string;
}

export interface WrkfEffectClaimResult {
  effects: WrkfEffect[];
  leaseToken: string;
  leaseExpiresAt?: string;
}

export interface WrkfEffectAckParams {
  effectId: string;
  leaseToken: string;
  receipt?: unknown;
}

export interface WrkfEffectFailParams {
  effectId: string;
  leaseToken: string;
  reason: string;
  retryable?: boolean;
}

export interface WrkfEffectRetryParams {
  effectId: string;
}

export interface WrkfEffectDeliverParams {
  effectId?: string;
  task?: string;
  instanceId?: string;
  adapter?: string;
}
