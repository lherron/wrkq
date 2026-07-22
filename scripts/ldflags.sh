#!/usr/bin/env bash
# Emit the Go -ldflags string that injects version metadata into the wrkq
# binaries. Version is derived from git tags; commit/date are stamped at build.
set -euo pipefail

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

pkgs=(
  github.com/lherron/wrkq/internal/admincli
  github.com/lherron/wrkq/internal/rpccli
  github.com/lherron/wrkq/internal/wrkfcli
)

flags=()
for p in "${pkgs[@]}"; do
  flags+=("-X" "${p}.Version=${VERSION}")
  flags+=("-X" "${p}.GitCommit=${COMMIT}")
  flags+=("-X" "${p}.BuildDate=${BUILD_DATE}")
done

echo "${flags[*]}"
