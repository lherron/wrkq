#!/usr/bin/env bash
# compare-rpccli.sh — convenience entrypoint for the RPC-backed mirror parity
# checks. The actual logic is the data-driven Go harness in
# internal/rpccli/parity_test.go (TestParity): old `wrkq <cmd>` vs new
# `wrkq-rpccli <cmd>`, byte-comparing exit + stdout + stderr and the durable task
# snapshot for mutating commands. Adding a command means appending a row to
# parityCases there — never editing a shell script.
#
# Seam-smoke (cat) + guard tests live alongside in the same package.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "Running RPC-backed mirror parity + transport + guard tests..."
go test -tags sqlite_fts5 ./internal/rpccli/ \
  -run 'TestParity|TestCoreRuleImportGuard|TestInProcessTransport|TestTransportEquivalence' \
  -v
