# wrkq Agent-Enablement Changelog

Target-local retro carrier for wrkq's agent-enablement sensors, workflow
mechanisms, and documentation carriers. Add one dated entry after a pass changes
the loop. Keep release notes elsewhere.

## 2026-07-22 — Canonical adoption probe follows the supported store boundary

- **Changed:** `scripts/check-wrkf-adoption.sh` now reads the canonical adoption
  canary through `wrkf` and its configured RPC/store transport. It no longer
  assumes that canonical workflow state lives in a repo-local SQLite file.
- **Proof:** the probe requires durable task `T-06783` to be a closed/done
  `wrkq-simple-task@5` instance with at least three completed runs spanning the
  coordinator, implementer, and tester roles.
- **Promote or sunset:** target-local. The canary selector and expected template
  remain wrkq-specific; the supported-transport rule is the reusable lesson.
- **Carrier lesson:** a usage sensor must cross the same public boundary as its
  users. Direct storage inspection silently becomes stale when deployment moves
  from embedded storage to a service.

## 2026-07-02 — AE convergence v2 Phase 1 probe wiring

- **Changed:** `just verify-full` now includes the canonical wrkf adoption probe
  after the temp-DB smokes; `just smoke` now includes a negative fixture proving
  the wrkf adoption smoke fails when `wrkf run start` does not create scoped
  workflow runs.
- **Proof:** `test/smoke-wrkf-adoption-negative.sh` expects the existing smoke
  to fail with `expected >= 3 workflow_runs`; `scripts/check-wrkf-adoption.sh`
  confirms the real configured DB has the scoped `T-04383`
  `wrkq-code-change@1` adoption signal.
- **Promote or sunset:** target-local for now. No reusable `checks/` template
  graduates from this pass; the adoption probe is too wrkq/wrkf-specific.
- **Carrier lesson:** usage probes belong in the slow backstop when they inspect
  real runtime state; the fast gate stays deterministic and repo-local.

## 2026-10-03 — Hook duration measurement (T-10096)

Added advisory whole-hook timing and detached hook.settled publication to the
existing pre-commit/pre-push paths. Original gates, order, status, stdin and
suppression semantics remain intact under `wrkq.verify-gate`. The isolated
34-case shell matrix proves failures, skips and publisher isolation, including
inherited GIT_* removal; `just verify` remains the landing gate. No new enforced
rule or service activation. Historical backfill is zero; the resident owns
measurement assessment and workflow closure. This instrument stays target-local.
