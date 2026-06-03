#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export GOPROXY=off
export GOSUMDB=off
export GOTOOLCHAIN=local
export CGO_ENABLED=1

# Conservative defaults for constrained, no-network sandboxes.
export GOFLAGS="${GOFLAGS:--mod=vendor -p=1}"
export CGO_CFLAGS="${CGO_CFLAGS:--O0 -g0}"

mkdir -p bin

PACKAGE_LIST="$(mktemp)"
trap 'rm -f "$PACKAGE_LIST"' EXIT

run_with_heartbeat() {
  local label="$1"
  shift
  local heartbeat_seconds="${AGENT_CHECK_HEARTBEAT_SECONDS:-10}"
  local heartbeat_pid status

  (
    while true; do
      sleep "$heartbeat_seconds"
      echo "still running: $label"
    done
  ) &
  heartbeat_pid=$!

  set +e
  "$@"
  status=$?
  set -e

  kill "$heartbeat_pid" 2>/dev/null || true
  wait "$heartbeat_pid" 2>/dev/null || true
  return "$status"
}

echo "== env =="
go version
go env GOVERSION GOOS GOARCH CGO_ENABLED GOPROXY GOSUMDB GOTOOLCHAIN GOFLAGS

echo "== vendor sanity =="
test -d vendor
test -f vendor/modules.txt
go list -mod=vendor ./... >"$PACKAGE_LIST"

echo "== compile packages =="
while IFS= read -r pkg; do
  test -n "$pkg" || continue
  echo "compile $pkg"
  run_with_heartbeat "compile $pkg" go test -tags sqlite_fts5 -run '^$' "$pkg"
done <"$PACKAGE_LIST"

echo "== build binaries =="
run_with_heartbeat "build wrkq" go build -tags sqlite_fts5 -o bin/wrkq ./cmd/wrkq
run_with_heartbeat "build wrkf" go build -tags sqlite_fts5 -o bin/wrkf ./cmd/wrkf
run_with_heartbeat "build wrkqadm" go build -tags sqlite_fts5 -o bin/wrkqadm ./cmd/wrkqadm
run_with_heartbeat "build wrkqd" go build -tags sqlite_fts5 -o bin/wrkqd ./cmd/wrkqd

echo "== smoke wrkf =="
run_with_heartbeat "smoke wrkf" test/smoke-wrkf.sh

echo "agent-check: ok"
