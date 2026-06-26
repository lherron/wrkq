#!/usr/bin/env bash
# compare-rpccli-pretty.sh — focused byte-parity check for the five base
# RPC mirror commands that expose `--pretty`.
#
# This intentionally reuses the data-driven Go oracle in
# internal/rpccli/parity_test.go instead of duplicating comparison logic here.
# The selected subtests are one canonical --pretty case per command:
#   - cat/pretty
#   - ls/pretty
#   - find/pretty
#   - tree/pretty
#   - search/pretty
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "Running RPC-backed mirror base-command --pretty byte parity checks..."
go test -tags sqlite_fts5 ./internal/rpccli/ \
  -run '^TestParity$/(^cat$|^ls$|^find$|^tree$|^search$)/^pretty$' \
  -count=1 \
  -v
