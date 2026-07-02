# wrkq Agent-Enablement Changelog

Target-local retro carrier for wrkq's agent-enablement sensors, workflow
mechanisms, and documentation carriers. Add one dated entry after a pass changes
the loop. Keep release notes elsewhere.

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
