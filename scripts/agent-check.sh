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

echo "== env =="
go version
go env GOVERSION GOOS GOARCH CGO_ENABLED GOPROXY GOSUMDB GOTOOLCHAIN GOFLAGS

echo "== vendor sanity =="
test -d vendor
test -f vendor/modules.txt
go list -mod=vendor ./... >/dev/null

echo "== compile packages =="
go test -tags sqlite_fts5 -run '^$' ./...

echo "== build binaries =="
go build -tags sqlite_fts5 -o bin/wrkq ./cmd/wrkq
go build -tags sqlite_fts5 -o bin/wrkf ./cmd/wrkf
go build -tags sqlite_fts5 -o bin/wrkqadm ./cmd/wrkqadm
go build -tags sqlite_fts5 -o bin/wrkqd ./cmd/wrkqd

echo "== smoke wrkf =="
test/smoke-wrkf.sh

echo "agent-check: ok"
